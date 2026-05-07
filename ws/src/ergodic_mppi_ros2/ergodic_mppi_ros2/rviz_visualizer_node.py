"""RViz visualizer for X500 ergodic MPPI SITL runs."""

from __future__ import annotations

import math
import os
from dataclasses import dataclass
from typing import Dict, Optional
from xml.etree import ElementTree as ET

import jax
import jax.numpy as jnp
import numpy as np
import rclpy
from geometry_msgs.msg import Point, Quaternion
from nav_msgs.msg import OccupancyGrid
from px4_msgs.msg import VehicleLocalPosition
from rclpy.node import Node
from rclpy.qos import DurabilityPolicy, HistoryPolicy, QoSProfile, ReliabilityPolicy
from visualization_msgs.msg import Marker, MarkerArray

from ergodic_mppi_ros2.frame_utils import (
    AnchorState,
    compute_anchor_map_from_drones_config,
    map_position_from_px4,
)
from ergodic_mppi_ros2.params_loader import load_mppi_params
from ergodic_mppi_ros2.stein import pdf
from ergodic_mppi_ros2.topic_utils import instance_to_prefix, topic_with_prefix


@dataclass
class MeshConfig:
    resource: str
    scale: np.ndarray
    xyz_offset: np.ndarray
    rpy_offset: np.ndarray


@dataclass
class InstanceState:
    map_xy: Optional[np.ndarray] = None
    z_up: float = 0.0
    heading: float = 0.0


def _parse_instances(raw: str, default_n: int) -> list[int]:
    raw_instances = raw.strip()
    if raw_instances:
        return sorted(int(x.strip()) for x in raw_instances.split(",") if x.strip())
    return list(range(default_n))


def _parse_pose_text(text: str) -> np.ndarray:
    values = [float(v) for v in text.strip().split() if v]
    if len(values) == 3:
        values = values + [0.0, 0.0, 0.0]
    if len(values) != 6:
        raise ValueError(f"Expected pose with 6 values, got {values}")
    return np.array(values, dtype=float)


def _rpy_to_matrix(rpy: np.ndarray) -> np.ndarray:
    roll, pitch, yaw = rpy.tolist()
    cr = math.cos(roll)
    sr = math.sin(roll)
    cp = math.cos(pitch)
    sp = math.sin(pitch)
    cy = math.cos(yaw)
    sy = math.sin(yaw)
    return np.array(
        [
            [cy * cp, cy * sp * sr - sy * cr, cy * sp * cr + sy * sr],
            [sy * cp, sy * sp * sr + cy * cr, sy * sp * cr - cy * sr],
            [-sp, cp * sr, cp * cr],
        ],
        dtype=float,
    )


def _matrix_to_quat(matrix: np.ndarray) -> np.ndarray:
    trace = float(np.trace(matrix))
    if trace > 0.0:
        s = math.sqrt(trace + 1.0) * 2.0
        qw = 0.25 * s
        qx = (matrix[2, 1] - matrix[1, 2]) / s
        qy = (matrix[0, 2] - matrix[2, 0]) / s
        qz = (matrix[1, 0] - matrix[0, 1]) / s
    else:
        if matrix[0, 0] > matrix[1, 1] and matrix[0, 0] > matrix[2, 2]:
            s = math.sqrt(1.0 + matrix[0, 0] - matrix[1, 1] - matrix[2, 2]) * 2.0
            qw = (matrix[2, 1] - matrix[1, 2]) / s
            qx = 0.25 * s
            qy = (matrix[0, 1] + matrix[1, 0]) / s
            qz = (matrix[0, 2] + matrix[2, 0]) / s
        elif matrix[1, 1] > matrix[2, 2]:
            s = math.sqrt(1.0 + matrix[1, 1] - matrix[0, 0] - matrix[2, 2]) * 2.0
            qw = (matrix[0, 2] - matrix[2, 0]) / s
            qx = (matrix[0, 1] + matrix[1, 0]) / s
            qy = 0.25 * s
            qz = (matrix[1, 2] + matrix[2, 1]) / s
        else:
            s = math.sqrt(1.0 + matrix[2, 2] - matrix[0, 0] - matrix[1, 1]) * 2.0
            qw = (matrix[1, 0] - matrix[0, 1]) / s
            qx = (matrix[0, 2] + matrix[2, 0]) / s
            qy = (matrix[1, 2] + matrix[2, 1]) / s
            qz = 0.25 * s
    quat = np.array([qw, qx, qy, qz], dtype=float)
    norm = np.linalg.norm(quat)
    if norm > 0.0:
        quat /= norm
    return quat


def _resolve_mesh_resource(model_dir: str, models_root: str, uri: str) -> str:
    if uri.startswith("file://"):
        return uri

    if uri.startswith("model://"):
        rel = uri[len("model://") :]
        parts = rel.split("/", 1)
        model_name = parts[0]
        rel_path = parts[1] if len(parts) > 1 else ""
        model_base = os.path.join(models_root, model_name) if model_name else model_dir
        mesh_path = os.path.join(model_base, rel_path) if rel_path else model_base
    elif os.path.isabs(uri):
        mesh_path = uri
    else:
        mesh_path = os.path.join(model_dir, uri)
    return f"file://{os.path.abspath(mesh_path)}"


def _resolve_include_sdf_path(model_dir: str, models_root: str, uri: str) -> str | None:
    if not uri.startswith("model://"):
        return None

    rel = uri[len("model://") :]
    parts = rel.split("/", 1)
    model_name = parts[0]
    if not model_name:
        return None

    subpath = parts[1] if len(parts) > 1 else ""
    include_base = os.path.join(models_root, model_name)
    if not subpath:
        return os.path.join(include_base, "model.sdf")

    if subpath.endswith(".sdf"):
        return os.path.join(include_base, subpath)
    return os.path.join(include_base, subpath, "model.sdf")


def _read_mesh_from_sdf_file(
    sdf_path: str,
    model_dir: str,
    models_root: str,
    preferred_link_name: str,
    preferred_visual_name: str,
    visited: set[str],
    searched_sdfs: list[str],
) -> MeshConfig | None:
    sdf_realpath = os.path.realpath(sdf_path)
    if sdf_realpath in visited:
        return None
    visited.add(sdf_realpath)
    searched_sdfs.append(sdf_realpath)

    if not os.path.exists(sdf_realpath):
        return None

    tree = ET.parse(sdf_realpath)
    root = tree.getroot()

    for link in root.iter("link"):
        link_name = str(link.get("name") or "").strip()
        if preferred_link_name and preferred_link_name != link_name:
            continue

        for visual in link.findall("visual"):
            visual_name = str(visual.get("name") or "").strip()
            if preferred_visual_name and preferred_visual_name != visual_name:
                continue

            geometry = visual.find("geometry")
            if geometry is None:
                continue
            mesh = geometry.find("mesh")
            if mesh is None:
                continue
            uri_el = mesh.find("uri")
            if uri_el is None or not uri_el.text:
                continue

            scale = np.array([1.0, 1.0, 1.0], dtype=float)
            scale_el = mesh.find("scale")
            if scale_el is not None and scale_el.text:
                vals = [float(v) for v in scale_el.text.split() if v]
                if len(vals) == 3:
                    scale = np.array(vals, dtype=float)

            xyz_offset = np.array([0.0, 0.0, 0.0], dtype=float)
            rpy_offset = np.array([0.0, 0.0, 0.0], dtype=float)
            pose_el = visual.find("pose")
            if pose_el is not None and pose_el.text:
                pose_vals = _parse_pose_text(pose_el.text)
                xyz_offset = pose_vals[:3]
                rpy_offset = pose_vals[3:]

            return MeshConfig(
                resource=_resolve_mesh_resource(model_dir, models_root, uri_el.text.strip()),
                scale=scale,
                xyz_offset=xyz_offset,
                rpy_offset=rpy_offset,
            )

    for include in root.iter("include"):
        uri_el = include.find("uri")
        if uri_el is None or not uri_el.text:
            continue
        include_sdf = _resolve_include_sdf_path(model_dir, models_root, uri_el.text.strip())
        if include_sdf is None:
            continue

        include_model_dir = os.path.dirname(include_sdf)
        mesh_cfg = _read_mesh_from_sdf_file(
            sdf_path=include_sdf,
            model_dir=include_model_dir,
            models_root=models_root,
            preferred_link_name=preferred_link_name,
            preferred_visual_name=preferred_visual_name,
            visited=visited,
            searched_sdfs=searched_sdfs,
        )
        if mesh_cfg is not None:
            return mesh_cfg

    return None


def _read_mesh_from_sdf(
    model_dir: str,
    model_sdf: str,
    preferred_link_name: str,
    preferred_visual_name: str,
) -> MeshConfig:
    model_dir_abs = os.path.abspath(model_dir)
    sdf_path = os.path.join(model_dir_abs, model_sdf)
    if not os.path.exists(sdf_path):
        raise FileNotFoundError(f"SDF not found: {sdf_path}")

    searched_sdfs: list[str] = []
    mesh_cfg = _read_mesh_from_sdf_file(
        sdf_path=sdf_path,
        model_dir=model_dir_abs,
        models_root=os.path.dirname(model_dir_abs),
        preferred_link_name=preferred_link_name,
        preferred_visual_name=preferred_visual_name,
        visited=set(),
        searched_sdfs=searched_sdfs,
    )
    if mesh_cfg is not None:
        return mesh_cfg

    searched = ", ".join(searched_sdfs) if searched_sdfs else sdf_path
    raise ValueError(f"No mesh visual found while scanning SDF files: {searched}")


class ErgodicMppiRvizVisualizer(Node):
    """Publish robot mesh, trails, obstacles, and target density for RViz."""

    def __init__(self) -> None:
        super().__init__("ergodic_mppi_rviz_visualizer")

        self.declare_parameter("px4_instances", "1,2")
        self.declare_parameter("num_robots", 1)
        self.declare_parameter("frame_id", "map")
        self.declare_parameter("publish_rate_hz", 20.0)
        self.declare_parameter("pose_topic", "/fmu/out/vehicle_local_position")
        self.declare_parameter("robot_marker_topic", "/ergodic_mppi_viz/robots")
        self.declare_parameter("trail_topic", "/ergodic_mppi_viz/trails")
        self.declare_parameter("obstacle_topic", "/ergodic_mppi_viz/obstacles")
        self.declare_parameter("density_topic", "/ergodic_mppi_viz/target_density")
        self.declare_parameter("mppi_config_path", "")
        self.declare_parameter("drones_config_path", "/config/drones_x500_mppi.yaml")
        self.declare_parameter("anchor_base", 50.0)

        self.declare_parameter(
            "model_dir",
            "/opt/PX4-Autopilot/Tools/simulation/gz/models/x500",
        )
        self.declare_parameter("model_sdf", "model.sdf")
        self.declare_parameter("mesh_resource", "")
        self.declare_parameter("mesh_link_name", "")
        self.declare_parameter("mesh_visual_name", "")
        self.declare_parameter("mesh_scale", [1.0, 1.0, 1.0])
        self.declare_parameter("mesh_xyz_offset", [0.0, 0.0, 0.0])
        self.declare_parameter("mesh_rpy_offset", [0.0, 0.0, 0.0])

        self.declare_parameter("trail_enable", True)
        self.declare_parameter("trail_z_mode", "altitude")
        self.declare_parameter("trail_max_points", 1200)
        self.declare_parameter("trail_min_dist", 0.25)
        self.declare_parameter("trail_width", 0.18)
        self.declare_parameter("trail_alpha", 0.9)

        self.declare_parameter("obstacle_height_m", 12.0)
        self.declare_parameter("obstacle_alpha", 0.25)

        n = int(self.get_parameter("num_robots").value or 1)
        raw_instances = str(self.get_parameter("px4_instances").value or "")
        self.instances = _parse_instances(raw_instances, n)

        self.frame_id = str(self.get_parameter("frame_id").value or "map")
        self.trail_enable = bool(self.get_parameter("trail_enable").value)
        self.trail_z_mode = str(self.get_parameter("trail_z_mode").value or "altitude")
        if self.trail_z_mode not in ("altitude", "ground"):
            self.trail_z_mode = "altitude"
        self.trail_max_points = int(self.get_parameter("trail_max_points").value or 1200)
        self.trail_min_dist = float(self.get_parameter("trail_min_dist").value or 0.25)
        self.trail_width = float(self.get_parameter("trail_width").value or 0.18)
        self.trail_alpha = float(self.get_parameter("trail_alpha").value or 0.9)

        mppi_config_path = str(self.get_parameter("mppi_config_path").value or "").strip()
        if not mppi_config_path:
            raise ValueError("mppi_config_path must be provided")
        self.params = load_mppi_params(mppi_config_path)

        drones_config_path = str(
            self.get_parameter("drones_config_path").value or ""
        ).strip()
        anchor_base = float(self.get_parameter("anchor_base").value or 50.0)
        anchor_map = compute_anchor_map_from_drones_config(drones_config_path, anchor_base)
        self.anchor_states: Dict[int, AnchorState] = {
            inst: AnchorState(px4_anchor_xy=anchor_map.get(inst)) for inst in self.instances
        }

        self.states: Dict[int, InstanceState] = {inst: InstanceState() for inst in self.instances}
        self.trails: Dict[int, list[np.ndarray]] = {inst: [] for inst in self.instances}

        model_dir = str(self.get_parameter("model_dir").value)
        model_sdf = str(self.get_parameter("model_sdf").value)
        mesh_resource = str(self.get_parameter("mesh_resource").value or "").strip()
        if mesh_resource:
            self.mesh_config = MeshConfig(
                resource=mesh_resource,
                scale=np.array([1.0, 1.0, 1.0], dtype=float),
                xyz_offset=np.array([0.0, 0.0, 0.0], dtype=float),
                rpy_offset=np.array([0.0, 0.0, 0.0], dtype=float),
            )
        else:
            self.mesh_config = _read_mesh_from_sdf(
                model_dir=model_dir,
                model_sdf=model_sdf,
                preferred_link_name=str(self.get_parameter("mesh_link_name").value or ""),
                preferred_visual_name=str(
                    self.get_parameter("mesh_visual_name").value or ""
                ),
            )
        self.mesh_config.scale = self.mesh_config.scale * np.array(
            self.get_parameter("mesh_scale").value or [1.0, 1.0, 1.0],
            dtype=float,
        )
        self.mesh_config.xyz_offset = self.mesh_config.xyz_offset + np.array(
            self.get_parameter("mesh_xyz_offset").value or [0.0, 0.0, 0.0],
            dtype=float,
        )
        self.mesh_config.rpy_offset = self.mesh_config.rpy_offset + np.array(
            self.get_parameter("mesh_rpy_offset").value or [0.0, 0.0, 0.0],
            dtype=float,
        )

        px4_qos = QoSProfile(
            reliability=ReliabilityPolicy.BEST_EFFORT,
            durability=DurabilityPolicy.TRANSIENT_LOCAL,
            history=HistoryPolicy.KEEP_LAST,
            depth=1,
        )
        pose_topic_suffix = str(
            self.get_parameter("pose_topic").value or "/fmu/out/vehicle_local_position"
        )
        for inst in self.instances:
            pose_topic = topic_with_prefix(instance_to_prefix(inst), pose_topic_suffix)
            self.create_subscription(
                VehicleLocalPosition,
                pose_topic,
                lambda msg, idx=inst: self._pose_cb(msg, idx),
                px4_qos,
            )

        self.robot_marker_pub = self.create_publisher(
            MarkerArray,
            str(self.get_parameter("robot_marker_topic").value),
            10,
        )
        self.trail_pub = self.create_publisher(
            MarkerArray,
            str(self.get_parameter("trail_topic").value),
            10,
        )

        static_qos = QoSProfile(
            reliability=ReliabilityPolicy.RELIABLE,
            durability=DurabilityPolicy.TRANSIENT_LOCAL,
            history=HistoryPolicy.KEEP_LAST,
            depth=1,
        )
        self.obstacle_pub = self.create_publisher(
            MarkerArray,
            str(self.get_parameter("obstacle_topic").value),
            static_qos,
        )
        self.density_pub = self.create_publisher(
            OccupancyGrid,
            str(self.get_parameter("density_topic").value),
            static_qos,
        )

        self._density_msg = self._build_density_message()
        self._obstacle_markers = self._build_obstacle_markers()
        self._publish_static_layers()

        publish_rate_hz = float(self.get_parameter("publish_rate_hz").value or 20.0)
        if publish_rate_hz <= 0.0:
            publish_rate_hz = 20.0
        self.create_timer(1.0 / publish_rate_hz, self._publish_dynamic_layers)

        self.get_logger().info(
            f"Ergodic MPPI RViz visualizer started: instances={self.instances}, "
            f"mesh={self.mesh_config.resource}"
        )

    def _pose_cb(self, msg: VehicleLocalPosition, instance: int) -> None:
        if not bool(getattr(msg, "xy_valid", False)):
            return
        raw_xy = np.array([float(msg.x), float(msg.y)], dtype=float)
        mapped = map_position_from_px4(raw_xy, self.anchor_states[instance])

        z_val = float(msg.z)
        z_up = -z_val if math.isfinite(z_val) else 0.0
        heading = float(msg.heading)
        if not math.isfinite(heading):
            heading = 0.0

        self.states[instance] = InstanceState(
            map_xy=mapped,
            z_up=z_up,
            heading=heading,
        )

    def _build_density_message(self) -> OccupancyGrid:
        x_min = float(self.params.map_x_limits[0])
        x_max = float(self.params.map_x_limits[1])
        y_min = float(self.params.map_y_limits[0])
        y_max = float(self.params.map_y_limits[1])
        resolution = float(self.params.resolution)

        width = max(int(round((x_max - x_min) / resolution)), 1)
        height = max(int(round((y_max - y_min) / resolution)), 1)

        xs = x_min + (np.arange(width, dtype=np.float32) + 0.5) * resolution
        ys = y_min + (np.arange(height, dtype=np.float32) + 0.5) * resolution
        grid_x, grid_y = np.meshgrid(xs, ys)
        points = np.stack([grid_x.ravel(), grid_y.ravel()], axis=1)

        points_jnp = jnp.asarray(points, dtype=jnp.float32)
        density = jax.vmap(pdf, in_axes=(0, None))(points_jnp, self.params.stein)
        density_map = np.asarray(density, dtype=np.float32).reshape((height, width))

        density_map = np.nan_to_num(density_map, nan=0.0, posinf=0.0, neginf=0.0)
        max_val = float(np.max(density_map)) if density_map.size else 1.0
        if max_val > 1e-9:
            density_map = density_map / max_val
        occ = np.clip(np.round(density_map * 100.0), 0, 100).astype(np.int8)

        msg = OccupancyGrid()
        msg.header.frame_id = self.frame_id
        msg.info.resolution = resolution
        msg.info.width = int(width)
        msg.info.height = int(height)
        msg.info.origin.position.x = x_min
        msg.info.origin.position.y = y_min
        msg.info.origin.orientation.w = 1.0
        msg.data = occ.flatten().tolist()
        return msg

    def _build_obstacle_markers(self) -> MarkerArray:
        markers = MarkerArray()
        obstacle_height = float(self.get_parameter("obstacle_height_m").value or 12.0)
        obstacle_alpha = float(self.get_parameter("obstacle_alpha").value or 0.25)

        for idx, obstacle in enumerate(np.asarray(self.params.obstacle_params.xyr)):
            marker = Marker()
            marker.header.frame_id = self.frame_id
            marker.ns = "ergodic_mppi_obstacles"
            marker.id = idx
            marker.type = Marker.CYLINDER
            marker.action = Marker.ADD
            marker.pose.position.x = float(obstacle[0])
            marker.pose.position.y = float(obstacle[1])
            marker.pose.position.z = obstacle_height * 0.5
            marker.pose.orientation.w = 1.0
            marker.scale.x = float(2.0 * obstacle[2])
            marker.scale.y = float(2.0 * obstacle[2])
            marker.scale.z = obstacle_height
            marker.color.r = 0.25
            marker.color.g = 0.20
            marker.color.b = 0.20
            marker.color.a = obstacle_alpha
            markers.markers.append(marker)
        return markers

    def _publish_static_layers(self) -> None:
        now = self.get_clock().now().to_msg()

        density = OccupancyGrid()
        density.header = self._density_msg.header
        density.info = self._density_msg.info
        density.data = list(self._density_msg.data)
        density.header.stamp = now
        density.info.map_load_time = now
        self.density_pub.publish(density)

        obstacle_array = MarkerArray()
        for marker in self._obstacle_markers.markers:
            stamped = Marker()
            stamped.header.frame_id = marker.header.frame_id
            stamped.header.stamp = now
            stamped.ns = marker.ns
            stamped.id = marker.id
            stamped.type = marker.type
            stamped.action = marker.action
            stamped.pose = marker.pose
            stamped.scale = marker.scale
            stamped.color = marker.color
            obstacle_array.markers.append(stamped)
        self.obstacle_pub.publish(obstacle_array)

    def _trail_color(self, inst: int) -> tuple[float, float, float]:
        palette = [
            (0.12, 0.47, 0.71),
            (1.00, 0.50, 0.05),
            (0.17, 0.63, 0.17),
            (0.84, 0.15, 0.16),
            (0.58, 0.40, 0.74),
            (0.55, 0.34, 0.29),
            (0.89, 0.47, 0.76),
            (0.50, 0.50, 0.50),
        ]
        return palette[inst % len(palette)]

    def _make_robot_marker(self, inst: int, state: InstanceState, stamp) -> Optional[Marker]:
        if state.map_xy is None:
            return None

        body_rpy = np.array([0.0, 0.0, state.heading], dtype=float)
        R_body = _rpy_to_matrix(body_rpy)
        R_offset = _rpy_to_matrix(self.mesh_config.rpy_offset)
        R = R_body @ R_offset
        quat = _matrix_to_quat(R)

        mesh_pos = np.array(
            [state.map_xy[0], state.map_xy[1], state.z_up],
            dtype=float,
        )
        mesh_pos = mesh_pos + (R_body @ self.mesh_config.xyz_offset)

        marker = Marker()
        marker.header.frame_id = self.frame_id
        marker.header.stamp = stamp
        marker.ns = "ergodic_mppi_x500"
        marker.id = inst
        marker.type = Marker.MESH_RESOURCE
        marker.action = Marker.ADD
        marker.mesh_resource = self.mesh_config.resource
        marker.mesh_use_embedded_materials = True
        marker.scale.x = float(self.mesh_config.scale[0])
        marker.scale.y = float(self.mesh_config.scale[1])
        marker.scale.z = float(self.mesh_config.scale[2])
        marker.pose.position.x = float(mesh_pos[0])
        marker.pose.position.y = float(mesh_pos[1])
        marker.pose.position.z = float(mesh_pos[2])
        marker.pose.orientation = Quaternion(
            x=float(quat[1]),
            y=float(quat[2]),
            z=float(quat[3]),
            w=float(quat[0]),
        )
        marker.color.r = 1.0
        marker.color.g = 1.0
        marker.color.b = 1.0
        marker.color.a = 1.0
        return marker

    def _update_trail(self, inst: int, state: InstanceState) -> None:
        if not self.trail_enable or state.map_xy is None:
            return
        z_val = 0.0 if self.trail_z_mode == "ground" else state.z_up
        point = np.array([state.map_xy[0], state.map_xy[1], z_val], dtype=float)

        trail = self.trails[inst]
        if trail:
            if np.linalg.norm(point - trail[-1]) < self.trail_min_dist:
                return
        trail.append(point)
        if self.trail_max_points > 0 and len(trail) > self.trail_max_points:
            del trail[0 : len(trail) - self.trail_max_points]

    def _make_trail_marker(self, inst: int, stamp) -> Optional[Marker]:
        if not self.trail_enable:
            return None
        trail = self.trails.get(inst, [])
        if len(trail) < 2:
            return None

        r, g, b = self._trail_color(inst)
        marker = Marker()
        marker.header.frame_id = self.frame_id
        marker.header.stamp = stamp
        marker.ns = "ergodic_mppi_trails"
        marker.id = inst
        marker.type = Marker.LINE_STRIP
        marker.action = Marker.ADD
        marker.scale.x = self.trail_width
        marker.color.r = r
        marker.color.g = g
        marker.color.b = b
        marker.color.a = self.trail_alpha
        marker.points = [
            Point(x=float(p[0]), y=float(p[1]), z=float(p[2])) for p in trail
        ]
        return marker

    def _publish_dynamic_layers(self) -> None:
        now = self.get_clock().now().to_msg()

        robot_array = MarkerArray()
        trail_array = MarkerArray()

        for inst in self.instances:
            state = self.states[inst]
            marker = self._make_robot_marker(inst, state, now)
            if marker is not None:
                robot_array.markers.append(marker)
            self._update_trail(inst, state)
            trail_marker = self._make_trail_marker(inst, now)
            if trail_marker is not None:
                trail_array.markers.append(trail_marker)

        if robot_array.markers:
            self.robot_marker_pub.publish(robot_array)
        if trail_array.markers:
            self.trail_pub.publish(trail_array)

        self._publish_static_layers()


def main(args=None) -> None:
    rclpy.init(args=args)
    node = ErgodicMppiRvizVisualizer()
    try:
        rclpy.spin(node)
    except KeyboardInterrupt:
        pass
    finally:
        node.destroy_node()
        rclpy.shutdown()


if __name__ == "__main__":
    main()
