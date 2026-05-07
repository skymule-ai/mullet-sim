"""Online multi-robot MPPI runtime helpers."""

import dataclasses

import jax
import jax.numpy as jnp

from ergodic_mppi_ros2.double_integrator import step as model_step
from ergodic_mppi_ros2.mppi_core import MPPIParams, adapt_lam, mppi_step


@jax.jit
def multi_robot_step(
    params: MPPIParams,
    x_all: jnp.ndarray,
    U_prev_all: jnp.ndarray,
    key_all: jnp.ndarray,
    lam_all: jnp.ndarray,
    histories_all: jnp.ndarray,
    surrogates_all: jnp.ndarray,
) -> tuple[jnp.ndarray, jnp.ndarray, jnp.ndarray, jnp.ndarray, jnp.ndarray, jnp.ndarray]:
    """Execute one centralized MPPI step for all robots."""
    num_robots = x_all.shape[0]
    cross_len = (num_robots - 1) * (params.T + params.history_len)
    params_base = dataclasses.replace(
        params,
        cross_particles_len=cross_len,
        cross_particles=jnp.zeros((cross_len, 2), dtype=jnp.float32),
    )

    new_u0 = []
    new_U = []
    new_keys = []
    new_lams = []
    new_histories = []
    new_surrogates = []

    for i in range(num_robots):
        other_flat = jnp.concatenate(
            [
                surrogates_all[:i].reshape(-1, 2),
                histories_all[:i].reshape(-1, 2),
                surrogates_all[i + 1 :].reshape(-1, 2),
                histories_all[i + 1 :].reshape(-1, 2),
            ],
            axis=0,
        )

        params_i = dataclasses.replace(
            params_base,
            lam=lam_all[i],
            history=histories_all[i],
            cross_particles=other_flat,
        )
        u0, U_next, key_next, _, _, traj_surrogate, w = mppi_step(
            params_i,
            U_prev_all[i],
            x_all[i],
            key_all[i],
        )

        new_u0.append(u0)
        new_U.append(U_next)
        new_keys.append(key_next)
        new_lams.append(adapt_lam(lam_all[i], w, params))
        new_histories.append(
            jnp.roll(histories_all[i], shift=-1, axis=0).at[-1].set(x_all[i, :2])
        )
        new_surrogates.append(traj_surrogate)

    u0_all = jnp.stack(new_u0, axis=0)
    U_all = jnp.stack(new_U, axis=0)
    key_next_all = jnp.stack(new_keys, axis=0)
    lam_next_all = jnp.stack(new_lams, axis=0)
    histories_next_all = jnp.stack(new_histories, axis=0)
    surrogates_next_all = jnp.stack(new_surrogates, axis=0)
    return (
        u0_all,
        U_all,
        key_next_all,
        lam_next_all,
        histories_next_all,
        surrogates_next_all,
    )


def acceleration_to_direction_command(
    x: jnp.ndarray,
    u: jnp.ndarray,
    dt: float,
) -> jnp.ndarray:
    """Convert MPPI acceleration control to `[vx, vy, 1]` direction command."""
    v_next = x[2:4] + dt * u[:2]
    speed = jnp.linalg.norm(v_next)

    candidate = jnp.where(speed > 1e-6, v_next / speed, jnp.zeros_like(v_next))
    accel_dir = jnp.where(
        jnp.linalg.norm(u[:2]) > 1e-6,
        u[:2] / jnp.linalg.norm(u[:2]),
        jnp.array([1.0, 0.0], dtype=jnp.float32),
    )
    direction = jnp.where(speed > 1e-3, candidate, accel_dir)
    return jnp.array([direction[0], direction[1], 1.0], dtype=jnp.float32)


def project_one_step_state(x: jnp.ndarray, u: jnp.ndarray, params: MPPIParams) -> jnp.ndarray:
    """Project one-step state with internal model (diagnostic/optional use)."""
    return model_step(x, u, params.model_params)
