# PX4 SITL image

Builds `ghcr.io/skymule-ai/px4-sitl`, the image `docker-compose.yml` runs: ROS 2 Jazzy on
Ubuntu 24.04, Gazebo Harmonic and PX4 SITL.

## PX4 version

The drones' Pixhawks run PX4 1.17.x, and every MULLET ROS image builds `px4_msgs` `release/1.17`.
This image builds the newest stable `v1.17.x` tag, never an `-rc`/`-beta`. When the Pixhawks move to
a newer 1.17.x, release that tag here. A new minor version (1.18) means bumping `px4_msgs` in every
ROS image at the same time.

## Build locally

```bash
docker build -t ghcr.io/skymule-ai/px4-sitl:v1.17.0 --build-arg PX4_VERSION=v1.17.0 image/
```

Build args: `ros_version` (default `jazzy`), `PX4_VERSION` (default `v1.17.0`).

## Release

Pushing a `px4-v*` tag runs `.github/workflows/release-px4-sitl.yml`:

```bash
git tag -a px4-v1.17.0 -m "Release PX4 v1.17.0 (ROS 2 Jazzy)"
git push origin px4-v1.17.0
```

It publishes `px4-sitl:px4-v1.17.0` and `px4-sitl:v1.17.0`; the second is what `docker-compose.yml`
pulls. The Humble images (`v1.16.1` and older) stay published for anything still pinned to them.

## User and extra packages

The image runs as `mullet-ai` (UID/GID 1000; the base's `ubuntu` user is removed to free that UID),
set by the `USERNAME`, `USER_UID` and `USER_GID` args in the Dockerfile.

Extra apt packages go in `additional-packages.list`, extra pip packages in
`additional-requirements.txt`, one per line. Rebuild after editing either.

Moved here from the former `sitl_builder` repository.
