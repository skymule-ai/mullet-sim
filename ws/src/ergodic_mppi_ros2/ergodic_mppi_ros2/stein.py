"""Stein gradient utilities for ergodic MPPI."""

import dataclasses
from dataclasses import dataclass

import jax
import jax.numpy as jnp
from jax.scipy.special import logsumexp


@jax.tree_util.register_dataclass
@dataclass(frozen=True)
class SteinParams:
    """Parameters for Stein-flow objective over a GMM target density."""

    means: jnp.ndarray
    cov_inv: jnp.ndarray
    log_weights: jnp.ndarray
    log_norm: jnp.ndarray
    A: jnp.ndarray
    ell_self: float
    ell_x: float
    weight_stein: float
    weight_pdf: float
    alpha_cross: float


def component_logpdf(x: jnp.ndarray, p: SteinParams) -> jnp.ndarray:
    dz = x[:2] - p.means
    quad = jnp.einsum("ki,kij,kj->k", dz, p.cov_inv, dz)
    return p.log_norm - 0.5 * quad


def logpdf(x: jnp.ndarray, p: SteinParams) -> jnp.ndarray:
    log_comps = p.log_weights + component_logpdf(x, p)
    return logsumexp(log_comps)


def score_pdf(x: jnp.ndarray, p: SteinParams) -> jnp.ndarray:
    return jax.grad(logpdf, argnums=0)(x, p)


def pdf(x: jnp.ndarray, p: SteinParams) -> jnp.ndarray:
    return jnp.exp(logpdf(x, p))


def kernel(x: jnp.ndarray, y: jnp.ndarray, p: SteinParams) -> jnp.ndarray:
    diff = x - y
    return jnp.exp(-jnp.dot(diff, diff) / p.ell_self)


def stein_grad_unit(x1: jnp.ndarray, x2: jnp.ndarray, p: SteinParams) -> jnp.ndarray:
    """Single particle Stein contribution."""
    ker_grad = jax.grad(kernel, argnums=0)(x2, x1, p)
    return kernel(x2, x1, p) * (p.A @ score_pdf(x2, p)) + (p.A @ ker_grad)


def stein_grad_state(x: jnp.ndarray, x_traj: jnp.ndarray, p: SteinParams) -> jnp.ndarray:
    """Average Stein gradient for one point against a trajectory."""
    stein_grads = jax.vmap(stein_grad_unit, in_axes=(None, 0, None))(x, x_traj, p)
    return jnp.mean(stein_grads, axis=0)


def stein_repulsion_unit(
    x1: jnp.ndarray, x2: jnp.ndarray, p: SteinParams
) -> jnp.ndarray:
    """Pure repulsion term between particles."""
    ker_grad = jax.grad(kernel, argnums=0)(x2, x1, p)
    return p.A @ ker_grad


def stein_repulsion_state(
    x: jnp.ndarray, x_traj: jnp.ndarray, p: SteinParams
) -> jnp.ndarray:
    """Average repulsion against cross-robot particles."""
    p_cross = dataclasses.replace(p, ell_self=p.ell_x)
    repulsions = jax.vmap(stein_repulsion_unit, in_axes=(None, 0, None))(
        x, x_traj, p_cross
    )
    return jnp.mean(repulsions, axis=0)
