"""Double-integrator dynamics used by MPPI."""

from dataclasses import dataclass

import jax
import jax.numpy as jnp

jax.config.update("jax_enable_x64", False)


@jax.tree_util.register_dataclass
@dataclass(frozen=True)
class DoubleIntegratorParams:
    """Model parameters for planar position + yaw double-integrator dynamics."""

    delta_t: float
    max_accel_lin_abs: float
    max_accel_ang_abs: float


def clamp(u: jnp.ndarray, p: DoubleIntegratorParams) -> jnp.ndarray:
    """Clamp controls to configured acceleration limits."""
    return jnp.array(
        [
            jnp.clip(u[0], -p.max_accel_lin_abs, p.max_accel_lin_abs),
            jnp.clip(u[1], -p.max_accel_lin_abs, p.max_accel_lin_abs),
            jnp.clip(u[2], -p.max_accel_ang_abs, p.max_accel_ang_abs),
        ]
    )


def step(x: jnp.ndarray, u: jnp.ndarray, p: DoubleIntegratorParams) -> jnp.ndarray:
    """Advance one step.

    State:
        x = [px, py, vx, vy, yaw, yaw_rate]
    Control:
        u = [ax, ay, yaw_accel]
    """
    u = clamp(u, p)

    px, py, vx, vy, yaw, yaw_rate = x
    ax, ay, yaw_accel = u
    dt = p.delta_t

    new_px = px + vx * dt + 0.5 * ax * dt**2
    new_py = py + vy * dt + 0.5 * ay * dt**2
    new_vx = vx + ax * dt
    new_vy = vy + ay * dt
    new_yaw = yaw + yaw_rate * dt + 0.5 * yaw_accel * dt**2
    new_yaw_rate = yaw_rate + yaw_accel * dt

    return jnp.array([new_px, new_py, new_vx, new_vy, new_yaw, new_yaw_rate])
