#!/usr/bin/env python3
"""Fleet launcher for PX4 SITL + Gazebo (runs inside the container).

Host tools (`run.sh`, the TUI) call the same script with:

    --print-env    validate the config and print shell variables for ``run.sh``
    --print-json   print the parsed config as JSON (problems on stderr), for the TUI
    --check-ports  fail if a UDP port the fleet will bind is already taken

YAML subset (no PyYAML in the image): top-level keys; one nested mapping
level; list-of-mappings (``fleet``, ``commands``); scalars; inline lists.
Not supported: anchors, multi-line values, ``#`` inside values.

Example:
    python3 scripts/spawn_fleet.py --config config/sessions/example_uav.yaml --print-env
"""
import argparse
import json
import os
import re
import shlex
import shutil
import signal
import socket
import subprocess
import sys
import time

DEFAULT_PX4_BIN = "px4"
DEFAULT_PX4_ROOT = "/opt/PX4-Autopilot"
DEFAULT_ROOTFS_BASE = "/opt/PX4-Autopilot/build/px4_sitl_default/rootfs"
PARAM_FILES_TO_RESET = ("parameters.bson", "parameters_backup.bson")

# Airframes shipped by the PX4 tree in the image are named '<autostart>_gz_<model>'.
# They are the source of truth for valid autostart/model pairs of that PX4 version.
AIRFRAME_DIRS = (
    "ROMFS/px4fmu_common/init.d-posix/airframes",
    "build/px4_sitl_default/etc/init.d-posix/airframes",
)
AIRFRAME_RE = re.compile(r"(\d+)_(gz_\w+)")

# PX4 params enforced for predictable SITL behaviour.
# simulation.px4_params and fleet[].params override them.
DEFAULT_PX4_PARAMS = {
    "COM_RC_IN_MODE": "4",  # Stick input disabled
    "NAV_RCL_ACT": "2",
    "NAV_DLL_ACT": "0",
    "COM_DL_LOSS_T": "300",
    "COM_RC_LOSS_T": "300",
}
# PX4's rcS applies PX4_PARAM_* before the airframe script, and the airframe's `param
# set-default` then replaces any value equal to PX4's compiled default (gz_x500 sets NAV_DLL_ACT 2
# over our 0, so arming wants a GCS). rcS sources `<airframe>.post` last: re-apply them there.
PARAM_POST_HOOK = """# mullet-sim: re-apply PX4_PARAM_* after the airframe's set-defaults.
env | while IFS='=' read -r line; do
  name=${line%%=*}
  case $name in
    "PX4_PARAM_"*) param set "${name#PX4_PARAM_}" "${line#*=}" ;;
  esac
done
"""
# Derived from the instance / ROS_DOMAIN_ID by PX4's rcS. Overriding them makes robots collide.
RESERVED_PX4_PARAMS = ("MAV_SYS_ID", "UXRCE_DDS_KEY", "UXRCE_DDS_DOM_ID")
PARAM_RE = re.compile(r"([A-Z][A-Z0-9_]*)=(\S+)")
NS_PREFIX_RE = re.compile(r"[A-Za-z][A-Za-z0-9_]*")

# PX4 sets MAV_SYS_ID = instance + 1 (max 255); mullet_core maps target_system the same way.
MAX_INSTANCE = 254
# PX4's px4-rc.mavlink gives every instance > 9 the same offboard remote port (14549).
MAX_INSTANCE_OWN_OFFBOARD_PORT = 9
# Local UDP ports each PX4 instance binds (px4-rc.mavlink): base + instance.
MAVLINK_LOCAL_PORT_BASES = {"GCS": 18570, "offboard": 14580, "payload": 14280, "gimbal": 13030}
MAX_ROS_DOMAIN_ID = 232

WINDOWS = ("px4", "xrce", "core", "code")
GPU_MODES = ("auto", "on", "off")


def _err(msg: str) -> None:
    """Write ``msg`` to stderr.

    Args:
        msg: Operator-visible error or warning line.
    """
    print(msg, file=sys.stderr)


def _run(cmd, env=None, stdout=None, stderr=None):
    """Start ``cmd`` in its own process group (so SIGTERM can kill the tree).

    Args:
        cmd: Executable and arguments.
        env: Environment mapping, or None for ``os.environ``.
        stdout: stdout handle passed to Popen.
        stderr: stderr handle passed to Popen.

    Returns:
        The child ``Popen`` object.
    """
    return subprocess.Popen(
        cmd, env=env, stdout=stdout, stderr=stderr, preexec_fn=os.setsid
    )


def _truthy(value) -> bool:
    """Return True for bool True or the strings 1/true/yes/on (any case).

    Args:
        value: Config or env value.

    Returns:
        Whether the value should be treated as enabled.
    """
    return value is True or str(value).strip().lower() in {"1", "true", "yes", "on"}


def _env_bool(name: str, default: bool = False) -> bool:
    """Read a boolean environment variable.

    Args:
        name: Variable name.
        default: Value when the variable is unset.

    Returns:
        Parsed flag.
    """
    raw = os.environ.get(name)
    if raw is None:
        return default
    return _truthy(raw)


def _as_list(value) -> list:
    """Normalize a YAML scalar or list into a list.

    Args:
        value: None, a scalar, or an already-parsed list.

    Returns:
        An empty list for None/empty string, otherwise a list of items.
    """
    if value is None or value == "":
        return []
    return value if isinstance(value, list) else [value]


def _resolve_px4_bin() -> str:
    """Return the PX4 binary path from ``PX4_BIN`` or the launcher default.

    Returns:
        Path or command name of the ``px4`` binary.
    """
    configured = os.environ.get("PX4_BIN", DEFAULT_PX4_BIN).strip()
    return configured or DEFAULT_PX4_BIN


def _resolve_rootfs_base(px4_bin: str) -> str:
    """Locate PX4's per-instance ``rootfs`` directory (persisted parameters).

    Args:
        px4_bin: PX4 binary path or command name.

    Returns:
        Absolute rootfs base, from ``PX4_ROOTFS_BASE``, the binary layout, or
        ``DEFAULT_ROOTFS_BASE``.
    """
    configured = os.environ.get("PX4_ROOTFS_BASE", "").strip()
    if configured:
        return configured

    candidate = px4_bin
    if "/" not in candidate:
        resolved = shutil.which(candidate)
        if not resolved:
            return DEFAULT_ROOTFS_BASE
        candidate = resolved

    candidate = os.path.realpath(candidate)
    parent = os.path.dirname(candidate)
    if os.path.basename(candidate) == "px4" and os.path.basename(parent) == "bin":
        return os.path.join(os.path.dirname(parent), "rootfs")

    return DEFAULT_ROOTFS_BASE


def _reset_instance_params(rootfs_base: str, instance: int) -> None:
    """Delete persisted PX4 parameter files for one instance.

    Args:
        rootfs_base: Directory that contains ``<instance>/parameters*.bson``.
        instance: PX4 instance number.
    """
    instance_rootfs = os.path.join(rootfs_base, str(instance))
    removed = []

    for filename in PARAM_FILES_TO_RESET:
        path = os.path.join(instance_rootfs, filename)
        try:
            os.remove(path)
            removed.append(filename)
        except FileNotFoundError:
            pass
        except Exception as exc:
            _err(f"instance {instance}: unable to remove '{path}': {exc}")

    if removed:
        print(
            f"[mullet] instance {instance}: reset persisted params ({', '.join(removed)})",
            flush=True,
        )


def _install_param_hook(rootfs_base: str, autostart, sim_model: str) -> None:
    """Make PX4 re-apply this launcher's ``PX4_PARAM_*`` after the airframe's defaults.

    Appends ``PARAM_POST_HOOK`` to ``<autostart>_<model>.post`` next to the airframe, which
    rcS sources last; an existing ``.post`` keeps its content.

    Args:
        rootfs_base: PX4's ``rootfs`` directory; the airframes live in its sibling ``etc``.
        autostart: Airframe autostart id (``PX4_SYS_AUTOSTART``).
        sim_model: Gazebo model of the airframe (``gz_x500``).
    """
    path = os.path.join(
        os.path.dirname(rootfs_base), "etc", "init.d-posix", "airframes", f"{autostart}_{sim_model}.post"
    )
    try:
        existing = ""
        if os.path.exists(path):
            with open(path, encoding="utf-8") as handle:
                existing = handle.read()
        if PARAM_POST_HOOK not in existing:
            with open(path, "a", encoding="utf-8") as handle:
                handle.write(("\n" if existing and not existing.endswith("\n") else "") + PARAM_POST_HOOK)
    except OSError as exc:
        _err(f"[mullet] warning: cannot write {path} ({exc}); airframe defaults may override PX4 params")


def _discover_airframes(px4_root: str) -> dict:
    """Map autostart id to gz model from the airframe files of the PX4 tree.

    Args:
        px4_root: PX4 source/build root inside the image (``/opt/PX4-Autopilot``).

    Returns:
        Dict of ``{autostart_id: "gz_<model>"}``. Missing directories are skipped.
    """
    airframes = {}
    for rel in AIRFRAME_DIRS:
        try:
            names = os.listdir(os.path.join(px4_root, rel))
        except OSError:
            continue
        for name in names:
            m = AIRFRAME_RE.fullmatch(name)
            if m:
                airframes[int(m.group(1))] = m.group(2)
    return airframes


def _parse_simple_yaml(path: str):
    """Parse the small YAML subset used by the fleet/session configs.

    Supported:
    - top-level keys holding a scalar or a nested block
    - mapping blocks, one extra level deep (e.g. simulation.xrce_agent)
    - list-of-mappings blocks whose items start with '- ' (fleet, commands)
    - scalar values (bool/int/float/str) and inline lists: [1, 2, 3]

    Not supported: anchors, multi-line values, '#' inside values.
    This avoids requiring PyYAML inside the SITL container.

    Args:
        path: Fleet/session YAML path.

    Returns:
        Nested dict (``simulation``, ``session``, ``commands``, ``fleet``, …).

    Raises:
        OSError: The file cannot be read.
        ValueError: A line is not in the supported subset.
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

    def parse_value(s: str):
        s = s.strip()
        if s.startswith("[") and s.endswith("]"):
            inner = s[1:-1].strip()
            return [parse_scalar(p) for p in inner.split(",")] if inner else []
        return parse_scalar(s)

    def split_kv(stripped: str, raw: str):
        if ":" not in stripped:
            raise ValueError(f"invalid line: {raw.rstrip()}")
        k, v = stripped.split(":", 1)
        return k.strip(), v.strip()

    root = {}
    section = subsection = item = None

    with open(path, "r", encoding="utf-8") as f:
        for raw in f:
            line = raw.split("#", 1)[0].rstrip()
            if line.strip() == "":
                continue

            indent = len(line) - len(line.lstrip(" "))
            stripped = line.strip()

            if indent == 0:
                k, v = split_kv(stripped, raw)
                section = subsection = item = None
                if v == "":
                    section = k
                else:
                    root[k] = parse_value(v)
                continue

            if section is None:
                raise ValueError(f"unexpected indentation: {raw.rstrip()}")

            if stripped == "-" or stripped.startswith("- "):
                items = root.setdefault(section, [])
                if not isinstance(items, list):
                    raise ValueError(f"'{section}' mixes mapping keys and list items")
                item = {}
                items.append(item)
                stripped = stripped[1:].strip()
                if not stripped:
                    continue

            block = root.get(section)
            if isinstance(block, list):
                if item is None:
                    raise ValueError(f"'{section}' entries must start with '- '")
                k, v = split_kv(stripped, raw)
                item[k] = parse_value(v)
                continue

            block = root.setdefault(section, {})
            k, v = split_kv(stripped, raw)
            if v == "":
                subsection = k
                block.setdefault(subsection, {})
                continue
            target = block[subsection] if subsection and indent >= 4 else block
            target[k] = parse_value(v)

    return root


def _format_pose(pose_list):
    """Format a 6-tuple pose as PX4's ``PX4_GZ_MODEL_POSE`` string.

    Args:
        pose_list: ``[x, y, z, roll, pitch, yaw]`` (metres / radians). Short
            lists are padded with zeros.

    Returns:
        Comma-separated floats, or the origin if empty.
    """
    if not pose_list:
        return "0,0,0,0,0,0"
    vals = list(pose_list)
    while len(vals) < 6:
        vals.append(0.0)
    vals = vals[:6]
    return ",".join(str(float(v)) for v in vals)


def _parse_params(value, label: str, errors: list) -> dict:
    """Parse a list of ``NAME=VALUE`` PX4 params.

    Args:
        value: Scalar or list from YAML.
        label: Prefix for error messages (e.g. ``simulation.px4_params``).
        errors: List appended in place when an entry is invalid or reserved.

    Returns:
        Mapping of parameter name to string value (invalid entries omitted).
    """
    params = {}
    for entry in _as_list(value):
        m = PARAM_RE.fullmatch(str(entry).strip())
        if not m:
            errors.append(f"{label}: expected NAME=VALUE, got {entry!r}")
            continue
        name, val = m.groups()
        if name in RESERVED_PX4_PARAMS:
            errors.append(
                f"{label}: {name} is derived from the instance/ROS_DOMAIN_ID and must not be set"
            )
            continue
        params[name] = val
    return params


def _validate(cfg, airframes: dict) -> bool:
    """Validate the config and resolve each vehicle's model/autostart in place.

    An empty ``airframes`` (host side) skips checks that need the PX4 tree of
    the image. Errors go to stderr with a ``Config error:`` prefix.

    Args:
        cfg: Parsed YAML dict (mutated: each fleet entry gets
            ``px4_sim_model`` / ``px4_sys_autostart``).
        airframes: ``{autostart: model}`` from the image, or ``{}`` on the host.

    Returns:
        True when there are no errors (warnings still print).
    """
    errors = []
    sim = cfg.get("simulation") or {}
    session = cfg.get("session") or {}
    fleet = cfg.get("fleet") or []

    prefix = sim.get("namespace_prefix")
    if prefix is not None and not NS_PREFIX_RE.fullmatch(str(prefix)):
        errors.append(
            f"simulation.namespace_prefix must match {NS_PREFIX_RE.pattern}, got {prefix!r}"
        )

    if "ros_domain_id" in sim:
        dom = sim["ros_domain_id"]
        if not isinstance(dom, int) or isinstance(dom, bool) or not 0 <= dom <= MAX_ROS_DOMAIN_ID:
            errors.append(
                f"simulation.ros_domain_id must be an integer in [0, {MAX_ROS_DOMAIN_ID}], got {dom!r}"
            )

    if "speed_factor" in sim:
        speed = sim["speed_factor"]
        if isinstance(speed, bool) or not isinstance(speed, (int, float)) or speed <= 0:
            errors.append(f"simulation.speed_factor must be > 0, got {speed!r}")

    _parse_params(sim.get("px4_params"), "simulation.px4_params", errors)

    gpu = session.get("gpu", "auto")
    if gpu not in GPU_MODES:
        errors.append(f"session.gpu must be one of {', '.join(GPU_MODES)}, got {gpu!r}")
    for window in _as_list(session.get("windows")):
        if window not in WINDOWS:
            errors.append(f"session.windows: unknown window {window!r} (known: {', '.join(WINDOWS)})")

    for idx, command in enumerate(cfg.get("commands") or []):
        if not str(command.get("run", "")).strip():
            errors.append(f"commands[{idx}]: missing 'run'")

    if not fleet:
        errors.append("no vehicles defined under 'fleet'")

    by_model = {model: autostart for autostart, model in airframes.items()}
    instances = []

    for idx, vehicle in enumerate(fleet):
        label = f"fleet[{idx}]"

        if "instance" not in vehicle:
            errors.append(f"{label}: missing required field 'instance'")
            continue
        inst = vehicle["instance"]
        if not isinstance(inst, int) or isinstance(inst, bool):
            errors.append(f"{label}: invalid instance value {inst!r}")
            continue

        label = f"{label} (instance {inst})"
        instances.append(inst)
        if not 0 <= inst <= MAX_INSTANCE:
            errors.append(f"{label}: instance must be in [0, {MAX_INSTANCE}] (MAV_SYS_ID = instance+1)")

        _parse_params(vehicle.get("params"), f"{label} params", errors)

        model = str(vehicle.get("model") or vehicle.get("px4_sim_model") or "").strip()
        autostart = vehicle.get("px4_sys_autostart")
        if autostart is not None and (not isinstance(autostart, int) or isinstance(autostart, bool)):
            errors.append(f"{label}: invalid px4_sys_autostart value {autostart!r}")
            continue

        if not model and autostart is None:
            errors.append(f"{label}: set 'model' (e.g. gz_x500)")
            continue

        if airframes:
            if not model:
                model = airframes.get(autostart, "")
                if not model:
                    errors.append(f"{label}: autostart {autostart} is not an airframe of this PX4 image")
                    continue
            elif autostart is None:
                autostart = by_model.get(model)
                if autostart is None:
                    errors.append(
                        f"{label}: model '{model}' is not in this PX4 image "
                        f"(available: {', '.join(sorted(by_model))})"
                    )
                    continue
            else:
                expected_model = airframes.get(autostart)
                if expected_model and model != expected_model:
                    errors.append(
                        f"{label}: autostart {autostart} expects model '{expected_model}', got '{model}'"
                    )
                    continue
                expected_autostart = by_model.get(model)
                if expected_autostart is not None and autostart != expected_autostart:
                    errors.append(
                        f"{label}: model '{model}' expects autostart {expected_autostart}, got {autostart}"
                    )
                    continue

        vehicle["px4_sim_model"] = model
        vehicle["px4_sys_autostart"] = autostart

    if len(set(instances)) != len(instances):
        errors.append(f"fleet instances must be unique, got: {sorted(instances)}")
    elif instances:
        sorted_instances = sorted(instances)
        # Gaps are allowed but may be unintentional.
        expected_contiguous = list(
            range(sorted_instances[0], sorted_instances[0] + len(sorted_instances))
        )
        if sorted_instances != expected_contiguous:
            _err(
                f"[mullet] warning: fleet instances are not contiguous: {sorted_instances} "
                f"(expected {expected_contiguous} if no gaps intended)"
            )
        shared = [i for i in sorted_instances if i > MAX_INSTANCE_OWN_OFFBOARD_PORT]
        if shared:
            _err(
                f"[mullet] warning: instances {shared} share PX4's MAVLink offboard remote port 14549; "
                f"per-robot MAVSDK/offboard links only work for instances <= {MAX_INSTANCE_OWN_OFFBOARD_PORT}"
            )

    for message in errors:
        _err(f"Config error: {message}")
    return not errors


def _fleet_udp_ports(cfg) -> dict:
    """Map UDP ports the fleet binds on the host (host networking) to owners.

    Args:
        cfg: Validated config dict.

    Returns:
        ``{port: owner_label}`` for MicroXRCEAgent and each PX4 MAVLink bind.
    """
    xrce = (cfg.get("simulation") or {}).get("xrce_agent") or {}
    ports = {}
    if str(xrce.get("transport", "udp4")).startswith("udp"):
        ports[int(xrce.get("port", 8888))] = "MicroXRCEAgent"
    for vehicle in cfg.get("fleet") or []:
        inst = int(vehicle["instance"])
        for what, base in MAVLINK_LOCAL_PORT_BASES.items():
            ports[base + inst] = f"instance {inst} MAVLink {what}"
    return ports


def _check_ports(cfg) -> bool:
    """Fail if any fleet UDP port is already bound on this host.

    Args:
        cfg: Validated config dict.

    Returns:
        True when every port is free.
    """
    busy = []
    for port, owner in sorted(_fleet_udp_ports(cfg).items()):
        with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as s:
            try:
                s.bind(("0.0.0.0", port))
            except OSError:
                busy.append(f"UDP {port} ({owner}) is already in use on this host")
    for message in busy:
        _err(f"Port error: {message}")
    return not busy


def _print_env(cfg, config_path: str) -> None:
    """Print the config as shell assignments for ``run.sh`` (``eval``).

    Args:
        cfg: Validated config dict.
        config_path: Original YAML path (used for ``MULLET_SESSION``).
    """
    sim = cfg.get("simulation") or {}
    session = cfg.get("session") or {}
    instances = sorted(int(v["instance"]) for v in cfg["fleet"])
    csv = ",".join(str(i) for i in instances)
    # Without namespace_prefix PX4 keeps its default /px4_<N> namespaces.
    ns_prefix = str(sim.get("namespace_prefix") or "px4")

    env = {
        "MULLET_SESSION": os.path.splitext(os.path.basename(config_path))[0],
        "MULLET_INSTANCES": " ".join(str(i) for i in instances),
        "MULLET_INSTANCES_CSV": csv,
        "MULLET_NS_PREFIX": ns_prefix,
        "MULLET_ROS_DOMAIN_ID": str(sim.get("ros_domain_id", "")),
        "MULLET_IMAGE": str(session.get("image", "")),
        "MULLET_RMW": str(session.get("rmw", "")),
        "MULLET_RVIZ": "1" if _truthy(session.get("rviz", False)) else "0",
        "MULLET_RVIZ_CONFIG": str(session.get("rviz_config", "")),
        "MULLET_QGC": "1" if _truthy(session.get("qgc", False)) else "0",
        "MULLET_FOXGLOVE": "1" if _truthy(session.get("foxglove", False)) else "0",
        "MULLET_BAG": "1" if _truthy(session.get("bag", False)) else "0",
        "MULLET_BAG_TOPICS": " ".join(str(t) for t in _as_list(session.get("bag_topics", "all"))),
        "MULLET_BUILD": "1" if _truthy(session.get("build", True)) else "0",
        "MULLET_WINDOWS": " ".join(_as_list(session.get("windows")) or WINDOWS),
    }
    if "gpu" in session:
        env["MULLET_GPU"] = str(session["gpu"])
    if "reset_params" in session:
        env["MULLET_RESET_PARAMS"] = "1" if _truthy(session["reset_params"]) else "0"

    commands = cfg.get("commands") or []
    env["MULLET_CMD_COUNT"] = str(len(commands))
    for i, command in enumerate(commands):
        run = str(command["run"]).replace("{instances}", csv).replace("{ns_prefix}", ns_prefix)
        env[f"MULLET_CMD_{i}"] = run
        env[f"MULLET_CMD_{i}_AUTORUN"] = "1" if _truthy(command.get("autorun", False)) else "0"

    for key, value in env.items():
        print(f"{key}={shlex.quote(value)}")


def _launch(cfg) -> int:
    """Start MicroXRCEAgent and every PX4 instance (container entrypoint).

    The lowest instance starts Gazebo; others set ``PX4_GZ_STANDALONE=1``.
    Writes ``$MULLET_RUNTIME_DIR/ready`` once the fleet is up.

    Args:
        cfg: Validated config with resolved ``px4_sim_model`` / autostart.

    Returns:
        Process exit code (0 on clean shutdown).
    """
    sim = cfg.get("simulation") or {}
    fleet = cfg["fleet"]

    world = sim.get("world", "default")
    headless = bool(sim.get("headless", False)) or _env_bool("MULLET_HEADLESS", default=False)
    config_gz_ip = str(sim.get("gz_ip", "127.0.0.1")).strip()
    # Same precedence as run.sh and sourcefile.sh: --domain > config > env > 0.
    override = os.environ.get("MULLET_DOMAIN_OVERRIDE", "").strip()
    if override:
        ros_domain_id, domain_source = int(override), "run.sh --domain"
    elif "ros_domain_id" in sim:
        ros_domain_id, domain_source = int(sim["ros_domain_id"]), "simulation.ros_domain_id"
    else:
        ros_domain_id = int(os.environ.get("ROS_DOMAIN_ID", "0"))
        domain_source = "ROS_DOMAIN_ID"
    ns_prefix = sim.get("namespace_prefix")
    speed_factor = sim.get("speed_factor")
    session_params = _parse_params(sim.get("px4_params"), "simulation.px4_params", [])

    xrce = sim.get("xrce_agent", {})
    agent_transport = str(xrce.get("transport", "udp4"))
    agent_port = int(xrce.get("port", 8888))

    env_base = os.environ.copy()
    env_base["ROS_DOMAIN_ID"] = str(ros_domain_id)
    # Align with the default make flow, which exports GZ_IP=127.0.0.1.
    # Precedence: existing env var > config value > launcher default.
    env_base["GZ_IP"] = os.environ.get("GZ_IP", config_gz_ip or "127.0.0.1")

    # If the Agent supports domain override, keep DDS aligned to ROS_DOMAIN_ID.
    # (Agent honors this only when clients use the reserved domain id 255.)
    env_base["XRCE_DOMAIN_ID_OVERRIDE"] = str(ros_domain_id)

    runtime_dir = os.environ.get("MULLET_RUNTIME_DIR", "/tmp/runtime")
    px4_bin = _resolve_px4_bin()
    px4_resolved = shutil.which(px4_bin) if "/" not in px4_bin else px4_bin
    rootfs_base = _resolve_rootfs_base(px4_bin)
    reset_params = _env_bool("MULLET_RESET_PARAMS", default=False)

    os.makedirs(runtime_dir, exist_ok=True)
    print(f"[mullet] runtime dir: {runtime_dir}", flush=True)
    print(f"[mullet] GZ_IP={env_base['GZ_IP']}", flush=True)
    print(f"[mullet] ROS_DOMAIN_ID={ros_domain_id} (from {domain_source})", flush=True)
    print(f"[mullet] PX4_BIN={px4_bin}", flush=True)
    if px4_resolved:
        print(f"[mullet] PX4_BIN_RESOLVED={px4_resolved}", flush=True)
    else:
        _err(f"[mullet] warning: PX4_BIN '{px4_bin}' not found on PATH")
    print(f"[mullet] PX4_ROOTFS_BASE={rootfs_base}", flush=True)
    print(
        f"[mullet] MULLET_RESET_PARAMS={'1' if reset_params else '0'}",
        flush=True,
    )

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
    # Per PX4 docs: the FIRST launched instance starts gz-server (do NOT set PX4_GZ_STANDALONE).
    # All subsequent instances must set PX4_GZ_STANDALONE=1 to attach to the running server.
    # Instances are launched in ascending order; the lowest instance number is the server-starter.
    fleet_sorted = sorted(fleet, key=lambda v: int(v["instance"]))

    server_inst = int(fleet_sorted[0]["instance"])
    print(
        f"[mullet] gz-server will be started by PX4 instance {server_inst}", flush=True
    )

    for idx, v in enumerate(fleet_sorted):
        inst = int(v["instance"])
        sys_autostart = v.get("px4_sys_autostart")
        sim_model = v["px4_sim_model"]
        if not sys_autostart or not sim_model:
            _err(f"Vehicle instance {inst}: cannot resolve model/autostart (no airframes found)")
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

        # Instance 0 keeps unprefixed topics, matching mullet_core's namespace rule.
        if ns_prefix and inst != 0:
            env["PX4_UXRCE_DDS_NS"] = f"{ns_prefix}_{inst}"

        if speed_factor is not None and float(speed_factor) != 1.0:
            env["PX4_SIM_SPEED_FACTOR"] = str(speed_factor)

        params = dict(DEFAULT_PX4_PARAMS)
        params.update(session_params)
        params.update(_parse_params(v.get("params"), "", []))
        for name, value in params.items():
            env[f"PX4_PARAM_{name}"] = value

        if reset_params:
            _reset_instance_params(rootfs_base, inst)
            env["PX4_PARAM_SYS_AUTOCONFIG"] = "1"
        _install_param_hook(rootfs_base, sys_autostart, sim_model)

        cmd = [px4_bin, "-i", str(inst)]
        print(
            f"[mullet] starting PX4 instance {inst}: PX4_SYS_AUTOSTART={sys_autostart} "
            f"PX4_SIM_MODEL={sim_model} PX4_GZ_MODEL_POSE={pose} "
            f"namespace={env.get('PX4_UXRCE_DDS_NS', 'px4 default')}",
            flush=True,
        )
        px4_log_path = os.path.join(runtime_dir, f"px4_{inst}.log")
        px4_log = open(px4_log_path, "a", encoding="utf-8")
        procs_open_files.append(px4_log)
        p = _run(cmd, env=env, stdout=px4_log, stderr=px4_log)
        procs.append(p)

        # Small stagger helps with Gazebo resources on startup
        time.sleep(0.8)

    # run.sh waits for this marker before building/launching ROS nodes.
    open(os.path.join(runtime_dir, "ready"), "w").close()

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


def main() -> int:
    """CLI entry: parse YAML, then print-env / print-json / check-ports / launch.

    Returns:
        0 on success, 1 on busy ports, 2 on config errors.
    """
    ap = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    ap.add_argument("--config", required=True)
    mode = ap.add_mutually_exclusive_group()
    mode.add_argument(
        "--print-env", action="store_true", help="validate and print shell variables for run.sh"
    )
    mode.add_argument(
        "--print-json", action="store_true", help="print the parsed config as JSON (for the TUI)"
    )
    mode.add_argument(
        "--check-ports", action="store_true", help="fail if a UDP port the fleet binds is in use"
    )
    args = ap.parse_args()

    try:
        cfg = _parse_simple_yaml(args.config)
    except (OSError, ValueError) as exc:
        _err(f"Config error: {exc}")
        return 2

    if args.print_json:
        # Printed even when invalid so the TUI can open a broken file and fix it.
        ok = _validate(cfg, airframes={})
        print(json.dumps(cfg))
        return 0 if ok else 2

    if args.print_env or args.check_ports:
        # Host side: airframes live in the image, so model checks happen at launch.
        if not _validate(cfg, airframes={}):
            return 2
        if args.check_ports:
            return 0 if _check_ports(cfg) else 1
        _print_env(cfg, args.config)
        return 0

    px4_root = os.environ.get("PX4_ROOT", DEFAULT_PX4_ROOT)
    airframes = _discover_airframes(px4_root)
    if not airframes:
        _err(f"[mullet] warning: no airframes found under {px4_root}; skipping model checks")
    if not _validate(cfg, airframes):
        return 2
    return _launch(cfg)


if __name__ == "__main__":
    raise SystemExit(main())
