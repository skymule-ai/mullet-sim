"""PX4-local to map-frame conversion helpers."""

from dataclasses import dataclass
from typing import Dict, Optional

import numpy as np
import yaml


@dataclass
class AnchorState:
    """Anchor conversion state for one PX4 instance."""

    px4_anchor_xy: Optional[np.ndarray] = None
    px4_to_map_offset_xy: Optional[np.ndarray] = None


def map_position_from_px4(px4_xy: np.ndarray, anchor_state: AnchorState) -> np.ndarray:
    """Convert a PX4 local XY position into shared map XY frame."""
    if anchor_state.px4_anchor_xy is None:
        return px4_xy
    if anchor_state.px4_to_map_offset_xy is None:
        anchor_state.px4_to_map_offset_xy = anchor_state.px4_anchor_xy - px4_xy
    return px4_xy + anchor_state.px4_to_map_offset_xy


def compute_anchor_map_from_drones_config(
    drones_config_path: str,
    anchor_base: float,
) -> Dict[int, np.ndarray]:
    """Build instance->anchor map from drones.yaml poses.

    Anchors preserve relative offsets in the drones config while translating
    into a positive map frame around `anchor_base`.
    """
    if not drones_config_path:
        return {}

    with open(drones_config_path, "r", encoding="utf-8") as file_obj:
        cfg = yaml.safe_load(file_obj) or {}

    fleet = cfg.get("fleet") or []
    if not fleet:
        return {}

    fleet_sorted = sorted(fleet, key=lambda drone: int(drone["instance"]))
    ref_pose = fleet_sorted[0].get("pose") or [0.0, 0.0]
    ref_x = float(ref_pose[0])
    ref_y = float(ref_pose[1])

    anchors: Dict[int, np.ndarray] = {}
    for drone in fleet_sorted:
        inst = int(drone["instance"])
        pose = drone.get("pose") or [0.0, 0.0]
        anchors[inst] = np.array(
            [
                anchor_base + (float(pose[0]) - ref_x),
                anchor_base + (float(pose[1]) - ref_y),
            ],
            dtype=float,
        )

    return anchors
