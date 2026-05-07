"""Core MPPI implementation adapted for ROS runtime integration."""

import dataclasses
from dataclasses import dataclass, field

import jax
import jax.numpy as jnp

from ergodic_mppi_ros2 import double_integrator as model
from ergodic_mppi_ros2.stein import SteinParams, stein_grad_state, stein_repulsion_state


@jax.tree_util.register_dataclass
@dataclass(frozen=True)
class ObstacleParams:
    """Circular obstacle set and penalties."""

    xyr: jnp.ndarray
    weight: float = field(metadata={"static": True})
    safe_distance: float = field(metadata={"static": True})


@jax.tree_util.register_dataclass
@dataclass(frozen=True)
class MPPIParams:
    """Controller parameters."""

    K: int = field(metadata={"static": True})
    T: int = field(metadata={"static": True})
    dim_u: int = field(metadata={"static": True})
    dim_x: int = field(metadata={"static": True})

    lam: float
    control_cost_coeff: float
    use_nominal: jnp.ndarray
    Sigma: jnp.ndarray
    Sigma_inv: jnp.ndarray
    map_x_limits: jnp.ndarray
    map_y_limits: jnp.ndarray
    resolution: float
    oom_cost: float
    stein: SteinParams
    model_params: model.DoubleIntegratorParams
    obstacle_params: ObstacleParams
    ess_target: float
    lam_min: float
    lam_max: float
    history_len: int = field(metadata={"static": True})
    history: jnp.ndarray = field(default_factory=lambda: jnp.zeros((1, 2), jnp.float32))
    cross_particles_len: int = field(metadata={"static": True}, default=0)
    cross_particles: jnp.ndarray = field(
        default_factory=lambda: jnp.zeros((0, 2), jnp.float32)
    )


def sample_epsilon(key: jax.Array, params: MPPIParams) -> tuple[jnp.ndarray, jax.Array]:
    """Sample rollout noise eps ~ N(0, Sigma) with shape (K, T, dim_u)."""
    key, sub = jax.random.split(key)
    eps = jax.random.multivariate_normal(
        sub,
        mean=jnp.zeros((params.dim_u,), dtype=jnp.float32),
        cov=params.Sigma,
        shape=(params.K, params.T),
        dtype=jnp.float32,
    )
    return eps, key


def _is_collided(x: jnp.ndarray, obs_params: ObstacleParams) -> jnp.ndarray:
    def check_obs(obstacle):
        dist = jnp.linalg.norm(x - obstacle[:2])
        return dist <= obstacle[2] + obs_params.safe_distance

    collided = jax.vmap(check_obs)(obs_params.xyr)
    return jnp.any(collided)


def stage_cost(x: jnp.ndarray, u: jnp.ndarray, params: MPPIParams) -> jnp.ndarray:
    """Constraint penalty stage cost."""
    del u
    collided = _is_collided(x[:2], params.obstacle_params)
    px, py = x[0], x[1]
    out_of_map = (
        (px < params.map_x_limits[0])
        | (px > params.map_x_limits[1])
        | (py < params.map_y_limits[0])
        | (py > params.map_y_limits[1])
    )
    return jnp.where(collided, params.obstacle_params.weight, 0.0) + jnp.where(
        out_of_map, params.oom_cost, 0.0
    )


def terminal_cost(x: jnp.ndarray, params: MPPIParams) -> jnp.ndarray:
    """Terminal cost placeholder."""
    del x, params
    return jnp.array(0.0, dtype=jnp.float32)


def batched_rollouts(
    params: MPPIParams,
    x0: jnp.ndarray,
    U_prev: jnp.ndarray,
    eps: jnp.ndarray,
    use_nominal: jnp.ndarray,
) -> tuple[jnp.ndarray, jnp.ndarray, jnp.ndarray]:
    """Roll out K trajectories with one scan over horizon T."""
    _, K, _ = eps.shape

    x = jnp.broadcast_to(x0, (K, params.dim_x))
    S = jnp.zeros((K,), dtype=jnp.float32)

    def step(carry, inputs):
        x_curr, S_curr = carry
        U_t, e_t = inputs

        v_t = jnp.where(use_nominal[:, None], U_t[None, :] + e_t, e_t)
        u_t = jax.vmap(model.clamp, in_axes=(0, None))(v_t, params.model_params)

        cross = params.control_cost_coeff * (
            (U_t[None, :] @ params.Sigma_inv) * v_t
        ).sum(axis=1)
        sc = jax.vmap(stage_cost, in_axes=(0, 0, None))(x_curr, u_t, params)

        S_next = S_curr + sc + cross
        x_next = jax.vmap(model.step, in_axes=(0, 0, None))(x_curr, u_t, params.model_params)
        pos = x_next[:, :2]
        return (x_next, S_next), (u_t, pos)

    (xT, S), (V, pos_traj) = jax.lax.scan(step, (x, S), (U_prev, eps))
    V = jnp.swapaxes(V, 0, 1)
    pos_traj = jnp.swapaxes(pos_traj, 0, 1)
    tc = jax.vmap(terminal_cost, in_axes=(0, None))(xT, params)
    return S + tc, V, pos_traj


def shift_U(U: jnp.ndarray) -> jnp.ndarray:
    """Shift warm-start trajectory by one step."""
    U_next = jnp.roll(U, shift=-1, axis=0)
    return U_next.at[-1].set(U[-1])


@jax.jit
def mppi_step(
    params: MPPIParams,
    U_prev: jnp.ndarray,
    x0: jnp.ndarray,
    key: jax.Array,
) -> tuple[jnp.ndarray, jnp.ndarray, jax.Array, jnp.ndarray, jnp.ndarray, jnp.ndarray, jnp.ndarray]:
    """Run one MPPI optimization step for a single robot."""
    eps, key = sample_epsilon(key, params)
    eps_T = jnp.swapaxes(eps, 0, 1)
    S, V, trajs = batched_rollouts(
        params=params,
        x0=x0,
        U_prev=U_prev,
        eps=eps_T,
        use_nominal=params.use_nominal,
    )

    spatial_trajs = trajs[:, :, :2]
    trajectory_surrogate = jnp.median(spatial_trajs, axis=0)

    diffs = trajectory_surrogate[:, None, :] - trajectory_surrogate[None, :, :]
    sq_dists = jnp.sum(diffs**2, axis=-1)
    ell_adapted = jnp.maximum(jnp.median(sq_dists), params.stein.ell_self)
    stein_adapted = dataclasses.replace(params.stein, ell_self=ell_adapted)
    params_ell = dataclasses.replace(params, stein=stein_adapted)

    self_particles = trajectory_surrogate
    ell_self = jax.vmap(stein_grad_state, in_axes=(0, None, None))(
        trajectory_surrogate, self_particles, params_ell.stein
    )

    if params.cross_particles_len > 0:
        ell_cross = jax.vmap(stein_repulsion_state, in_axes=(0, None, None))(
            trajectory_surrogate, params_ell.cross_particles, params_ell.stein
        )
        h_target = ell_self + params_ell.stein.alpha_cross * ell_cross
    else:
        h_target = ell_self

    S_flow = -jnp.einsum("ktn,tn->k", spatial_trajs, h_target)
    S = S + params.stein.weight_stein * S_flow

    rho = jnp.min(S)
    w_unnorm = jnp.exp(-(S - rho) / params.lam)
    w = w_unnorm / (jnp.sum(w_unnorm) + 1e-12)

    delta = V - U_prev[None, :, :]
    w_eps = jnp.einsum("k,ktu->tu", w, delta)
    U = U_prev + w_eps

    def opt_step(x, u):
        x_next = model.step(x, u, params.model_params)
        return x_next, x_next

    _, opt_traj = jax.lax.scan(opt_step, x0, U)
    u0 = U.at[0].get()
    U_next = shift_U(U)
    return u0, U_next, key, trajs, opt_traj, trajectory_surrogate, w


def adapt_lam(lam: jnp.ndarray, w: jnp.ndarray, params: MPPIParams) -> jnp.ndarray:
    """ESS-based multiplicative adaptation of lambda."""
    ess_rate = 0.05
    ess_frac = 1.0 / (jnp.sum(w**2) * params.K)
    lam_new = lam * jnp.exp(ess_rate * (params.ess_target - ess_frac))
    return jnp.clip(lam_new, params.lam_min, params.lam_max)
