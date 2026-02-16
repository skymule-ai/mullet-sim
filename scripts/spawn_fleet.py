#!/usr/bin/env python3
import argparse
import os
import shlex
import signal
import subprocess
import sys
import time


def _err(msg: str) -> None:
    print(msg, file=sys.stderr)


def _run(cmd, env=None, stdout=None, stderr=None):
    return subprocess.Popen(
        cmd, env=env, stdout=stdout, stderr=stderr, preexec_fn=os.setsid
    )


def _parse_simple_yaml(path: str):
    """Parse a very small subset of YAML used by config/drones.yaml.

    Supported:
    - top-level mapping keys
    - nested mapping (one level) for simulation / xrce_agent
    - list of dicts under `fleet`
    - scalar values (bool/int/float/str)
    - inline lists: [1, 2, 3]

    This avoids requiring PyYAML inside the SITL container.
    """

    def parse_scalar(s: str):
        s = s.strip()
        if s.lower() in ("true", "false"):
            return s.lower() == "true"
        try:
            if "." in s or "e" in s.lower():
                return float(s)
            return int(s)
        except Exception:
            pass
        if (s.startswith('"') and s.endswith('"')) or (
            s.startswith("'") and s.endswith("'")
        ):
            return s[1:-1]
        return s

    def parse_inline_list(s: str):
        s = s.strip()
        if not (s.startswith("[") and s.endswith("]")):
            return None
        inner = s[1:-1].strip()
        if inner == "":
            return []
        parts = [p.strip() for p in inner.split(",")]
        return [parse_scalar(p) for p in parts]

    root = {}
    cur_section = None
    cur_subsection = None
    fleet = []
    cur_item = None

    with open(path, "r", encoding="utf-8") as f:
        for raw in f:
            line = raw.split("#", 1)[0].rstrip("\n")
            if line.strip() == "":
                continue

            indent = len(line) - len(line.lstrip(" "))
            stripped = line.strip()

            if indent == 0 and stripped.endswith(":"):
                key = stripped[:-1]
                if key == "fleet":
                    cur_section = "fleet"
                    cur_subsection = None
                    cur_item = None
                else:
                    cur_section = key
                    root.setdefault(cur_section, {})
                    cur_subsection = None
                continue

            if cur_section == "fleet":
                if stripped.startswith("- "):
                    if cur_item is not None:
                        fleet.append(cur_item)
                    cur_item = {}
                    rest = stripped[2:].strip()
                    if rest:
                        if ":" not in rest:
                            raise ValueError(f"Invalid fleet item line: {raw}")
                        k, v = [x.strip() for x in rest.split(":", 1)]
                        lst = parse_inline_list(v)
                        cur_item[k] = lst if lst is not None else parse_scalar(v)
                else:
                    if cur_item is None:
                        raise ValueError("fleet entries must start with '- '")
                    if ":" not in stripped:
                        raise ValueError(f"Invalid fleet kv line: {raw}")
                    k, v = [x.strip() for x in stripped.split(":", 1)]
                    lst = parse_inline_list(v)
                    cur_item[k] = lst if lst is not None else parse_scalar(v)
                continue

            # non-fleet sections
            if ":" not in stripped:
                raise ValueError(f"Invalid yaml line: {raw}")
            k, v = [x.strip() for x in stripped.split(":", 1)]

            if v == "" and stripped.endswith(":"):
                # begin subsection
                cur_subsection = k
                root[cur_section].setdefault(cur_subsection, {})
                continue

            target = root[cur_section]
            if cur_subsection and indent >= 4:
                target = root[cur_section][cur_subsection]
            lst = parse_inline_list(v)
            target[k] = lst if lst is not None else parse_scalar(v)

    if cur_section == "fleet" and cur_item is not None:
        fleet.append(cur_item)
    root["fleet"] = fleet
    return root


def _format_pose(pose_list):
    if not pose_list:
        return "0,0,0,0,0,0"
    vals = list(pose_list)
    while len(vals) < 6:
        vals.append(0.0)
    vals = vals[:6]
    return ",".join(str(float(v)) for v in vals)


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--config", required=True)
    args = ap.parse_args()

    cfg = _parse_simple_yaml(args.config)
    sim = cfg.get("simulation", {})
    fleet = cfg.get("fleet", [])

    if not fleet:
        _err("No vehicles defined under 'fleet' in config")
        return 2

    instances = []
    for v in fleet:
        if "instance" not in v:
            _err(f"Vehicle missing 'instance': {v}")
            return 2
        instances.append(int(v["instance"]))
    if len(set(instances)) != len(instances):
        _err("All fleet.instance values must be unique")
        return 2

    world = sim.get("world", "default")
    headless = bool(sim.get("headless", False))
    ros_domain_id = int(
        sim.get("ros_domain_id", int(os.environ.get("ROS_DOMAIN_ID", "0")))
    )

    xrce = sim.get("xrce_agent", {})
    agent_transport = str(xrce.get("transport", "udp4"))
    agent_port = int(xrce.get("port", 8888))

    env_base = os.environ.copy()
    env_base["ROS_DOMAIN_ID"] = str(ros_domain_id)

    # If the Agent supports domain override, keep DDS aligned to ROS_DOMAIN_ID.
    # (Agent honors this only when clients use the reserved domain id 255.)
    env_base.setdefault("XRCE_DOMAIN_ID_OVERRIDE", str(ros_domain_id))

    runtime_dir = os.environ.get("MULLET_RUNTIME_DIR", "/workspace/.runtime")
    os.makedirs(runtime_dir, exist_ok=True)
    print(f"[mullet] runtime dir: {runtime_dir}", flush=True)

    # Start Micro XRCE agent (single agent for all PX4 instances)
    agent_cmd = ["MicroXRCEAgent", agent_transport, "-p", str(agent_port)]
    print(
        "[mullet] starting MicroXRCEAgent:",
        " ".join(shlex.quote(c) for c in agent_cmd),
        flush=True,
    )

    agent_log_path = os.path.join(runtime_dir, "xrce_agent.log")
    agent_log = open(agent_log_path, "a", encoding="utf-8")
    procs_open_files = [agent_log]
    agent_proc = _run(agent_cmd, env=env_base, stdout=agent_log, stderr=agent_log)

    procs = [agent_proc]

    def shutdown(signum=None, frame=None):
        for p in procs[::-1]:
            try:
                os.killpg(os.getpgid(p.pid), signal.SIGTERM)
            except Exception:
                pass
        # Give a moment, then hard kill
        time.sleep(1.0)
        for p in procs[::-1]:
            try:
                os.killpg(os.getpgid(p.pid), signal.SIGKILL)
            except Exception:
                pass

    signal.signal(signal.SIGINT, shutdown)
    signal.signal(signal.SIGTERM, shutdown)

    # Launch PX4 instances.
    # Per PX4 docs: first instance starts gz-server (do NOT set PX4_GZ_STANDALONE).
    # Subsequent instances set PX4_GZ_STANDALONE=1 to attach.
    fleet_sorted = sorted(fleet, key=lambda v: int(v["instance"]))

    for idx, v in enumerate(fleet_sorted):
        inst = int(v["instance"])
        sys_autostart = int(v.get("px4_sys_autostart", 0))
        if not sys_autostart:
            _err(f"Vehicle instance {inst} missing px4_sys_autostart")
            shutdown()
            return 2
        sim_model = str(v.get("px4_sim_model", ""))
        if not sim_model:
            _err(f"Vehicle instance {inst} missing px4_sim_model")
            shutdown()
            return 2
        pose = _format_pose(v.get("pose", []))

        env = env_base.copy()
        env["PX4_SYS_AUTOSTART"] = str(sys_autostart)
        env["PX4_SIM_MODEL"] = sim_model
        env["PX4_GZ_WORLD"] = str(world)
        env["PX4_GZ_MODEL_POSE"] = pose

        # Best-effort: allow overriding renderer if needed
        if "PX4_GZ_SIM_RENDER_ENGINE" in os.environ:
            env["PX4_GZ_SIM_RENDER_ENGINE"] = os.environ["PX4_GZ_SIM_RENDER_ENGINE"]

        if headless:
            env["HEADLESS"] = "1"

        if idx != 0:
            env["PX4_GZ_STANDALONE"] = "1"

        px4_bin = os.environ.get("PX4_BIN", "px4")
        cmd = [px4_bin, "-i", str(inst)]
        print(
            f"[mullet] starting PX4 instance {inst}: PX4_SYS_AUTOSTART={sys_autostart} PX4_SIM_MODEL={sim_model} PX4_GZ_MODEL_POSE={pose}",
            flush=True,
        )
        px4_log_path = os.path.join(runtime_dir, f"px4_{inst}.log")
        px4_log = open(px4_log_path, "a", encoding="utf-8")
        procs_open_files.append(px4_log)
        p = _run(cmd, env=env, stdout=px4_log, stderr=px4_log)
        procs.append(p)

        # Small stagger helps with Gazebo resources on startup
        time.sleep(0.8)

    # Wait on children
    exit_code = 0
    try:
        while True:
            for p in procs:
                rc = p.poll()
                if rc is not None:
                    # Any unexpected exit -> shut everything down
                    if p is agent_proc:
                        _err(f"MicroXRCEAgent exited with {rc}")
                    else:
                        _err(f"PX4 process exited with {rc}")
                    exit_code = rc if rc is not None else 1
                    raise KeyboardInterrupt
            time.sleep(1.0)
    except KeyboardInterrupt:
        shutdown()
    for f in procs_open_files:
        try:
            f.close()
        except Exception:
            pass
    return int(exit_code) if isinstance(exit_code, int) else 1


if __name__ == "__main__":
    raise SystemExit(main())
