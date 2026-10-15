# syntax=docker/dockerfile:1.7
#
# PX4 SITL image (shared deps stage)
# - Runs Tools/setup/ubuntu.sh exactly once (cached layer) in px4_deps
# - builder stage compiles PX4 (and Micro XRCE Agent)
# - runtime stage inherits deps (so shared libs are present) and copies artifacts
#
# Build args. PX4_VERSION tracks the drones' Pixhawk firmware line (1.17.x): build the newest
# stable v1.17.x tag. px4_msgs release/1.17 in the ROS images matches it.
ARG ros_version=jazzy
ARG PX4_VERSION=v1.17.0
ARG MICRO_XRCE_DDS_VERSION=v2.4.3

# User args (used in runtime)
ARG USERNAME=mullet-ai
ARG USER_UID=1000
ARG USER_GID=1000

# -------------------------
# 1) Common deps stage
# -------------------------
FROM osrf/ros:${ros_version}-desktop-full AS px4_deps
ARG ros_version
ARG PX4_VERSION

# Ubuntu 24.04 marks the system Python as externally managed (PEP 668); this image owns it.
ENV DEBIAN_FRONTEND=noninteractive \
    ROS_DISTRO=${ros_version} \
    PX4_VERSION=${PX4_VERSION} \
    PX4_DIR=/opt/PX4-Autopilot \
    PIP_BREAK_SYSTEM_PACKAGES=1

# Base OS packages:
# - keep here everything that both builder and runtime need
# - add small "ops" tools like wget/tmux here if you want them in the final image
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt/lists,sharing=locked \
    apt-get update \
    && apt-get install -y --no-install-recommends \
        bash \
        ca-certificates \
        git \
        net-tools \
        python3 \
        python3-pip \
        python3-setuptools \
        python3-wheel \
        sudo \
        tmux \
        wget \
    && rm -rf /var/lib/apt/lists/*

# Clone PX4 once (this layer invalidates only when PX4_VERSION changes)
RUN git clone --recursive --branch ${PX4_VERSION} \
    https://github.com/PX4/PX4-Autopilot.git ${PX4_DIR}

# Install PX4 dependencies ONCE (cached layer)
# This ensures runtime has the system libs needed by the built px4 binary.
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt/lists,sharing=locked \
    bash ${PX4_DIR}/Tools/setup/ubuntu.sh \
    && rm -rf /var/lib/apt/lists/*

# Install PX4 python requirements ONCE
RUN --mount=type=cache,target=/root/.cache/pip,sharing=locked \
    pip3 install -r ${PX4_DIR}/Tools/setup/requirements.txt \
    && pip3 install pyyaml jinja2

# Optional: extra apt packages (hook)
# Put additional apt packages (one per line) into image/additional-packages.list
COPY additional-packages.list /tmp/additional-packages.list
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt/lists,sharing=locked \
    if [ -s /tmp/additional-packages.list ]; then \
        apt-get update && xargs -r -a /tmp/additional-packages.list apt-get install -y --no-install-recommends; \
    fi \
    && rm -f /tmp/additional-packages.list \
    && rm -rf /var/lib/apt/lists/*

# Optional: extra pip requirements (hook)
# Put additional pip requirements into image/additional-requirements.txt
COPY additional-requirements.txt /tmp/additional-requirements.txt
RUN --mount=type=cache,target=/root/.cache/pip,sharing=locked \
    if [ -s /tmp/additional-requirements.txt ]; then \
        pip3 install -r /tmp/additional-requirements.txt; \
    fi \
    && rm -f /tmp/additional-requirements.txt


# -------------------------
# 2) Builder stage
# -------------------------
FROM px4_deps AS builder
ARG MICRO_XRCE_DDS_VERSION

# Build tooling needed only for compiling (not necessarily needed at runtime)
# If you want a smaller final image later, keep these here (not in px4_deps).
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt/lists,sharing=locked \
    apt-get update \
    && apt-get install -y --no-install-recommends \
        astyle \
        build-essential \
        ccache \
        cmake \
        cppcheck \
        file \
        g++ \
        gcc \
        gdb \
        lcov \
        libssl-dev \
        libxml2-dev \
        libxml2-utils \
        lsb-release \
        make \
        ninja-build \
        rsync \
        shellcheck \
        unzip \
        zip \
    && rm -rf /var/lib/apt/lists/*

ENV CCACHE_DIR=/ccache \
    CCACHE_MAXSIZE=5G \
    PATH=/usr/lib/ccache:$PATH

# Build Micro XRCE DDS Agent (installed into /usr/local)
ENV MICRO_XRCE_DIR=/opt/Micro-XRCE-DDS-Agent
RUN git clone --depth 1 -b ${MICRO_XRCE_DDS_VERSION} \
    https://github.com/eProsima/Micro-XRCE-DDS-Agent.git ${MICRO_XRCE_DIR}

WORKDIR ${MICRO_XRCE_DIR}
RUN mkdir -p build \
    && cd build \
    && cmake .. \
    && make -j"$(nproc)" \
    && make install \
    && ldconfig

# Build PX4 SITL once
ENV PX4_DIR=/opt/PX4-Autopilot
WORKDIR ${PX4_DIR}
RUN --mount=type=cache,target=/ccache,sharing=locked \
    ccache -z \
    && make px4_sitl_default \
    && ccache -s


# -------------------------
# 3) Runtime stage
# -------------------------
FROM px4_deps AS runtime
ARG USERNAME
ARG USER_UID
ARG USER_GID

# Copy PX4 tree for now (easy debug). Later you can copy only build artifacts.
# COPY --from=builder --chown=${USER_UID}:${USER_GID} ${PX4_DIR} ${PX4_DIR}
COPY --from=builder --chown=${USER_UID}:${USER_GID} \
  ${PX4_DIR}/build \
  ${PX4_DIR}/build


# Copy Micro XRCE Agent runtime install
COPY --from=builder /usr/local /usr/local
RUN ldconfig

# Create non-root user. Ubuntu 24.04 images ship user `ubuntu` on UID/GID 1000: drop it so
# the image user keeps that UID (it matches the host user for the mounted workspace).
RUN if id -u ubuntu >/dev/null 2>&1 && [ "$(id -u ubuntu)" = "${USER_UID}" ]; then \
        userdel -r ubuntu; \
    fi \
    && if getent group "${USER_GID}" >/dev/null 2>&1; then \
        GROUP_NAME="$(getent group "${USER_GID}" | cut -d: -f1)"; \
    else \
        GROUP_NAME="${USERNAME}"; \
        groupadd --gid "${USER_GID}" "${GROUP_NAME}"; \
    fi \
    && if id -u "${USERNAME}" >/dev/null 2>&1; then \
        usermod --uid "${USER_UID}" --gid "${USER_GID}" "${USERNAME}"; \
    else \
        useradd --uid "${USER_UID}" --gid "${USER_GID}" -m -s /bin/bash "${USERNAME}"; \
    fi \
    && mkdir -p "/home/${USERNAME}" \
    && chown -R "${USER_UID}:${USER_GID}" "/home/${USERNAME}" \
    && echo "${USERNAME} ALL=(root) NOPASSWD:ALL" > "/etc/sudoers.d/${USERNAME}" \
    && chmod 0440 "/etc/sudoers.d/${USERNAME}"

# Entrypoint
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# .bashrc: only ROS + PX4_DIR + PATH (no fragile PX4 setup scripts)
RUN BASHRC="/home/${USERNAME}/.bashrc" \
    && touch "${BASHRC}" \
    && if ! grep -q "source /opt/ros/${ROS_DISTRO}/setup.bash" "${BASHRC}"; then \
        printf '\nsource /opt/ros/%s/setup.bash\n' "${ROS_DISTRO}" >> "${BASHRC}"; \
    fi \
    && if ! grep -q "export PX4_DIR=/opt/PX4-Autopilot" "${BASHRC}"; then \
        printf 'export PX4_DIR=/opt/PX4-Autopilot\n' >> "${BASHRC}"; \
    fi \
    && if ! grep -q 'export PATH="$HOME/.local/bin:$PATH"' "${BASHRC}"; then \
        printf 'export PATH="$HOME/.local/bin:$PATH"\n' >> "${BASHRC}"; \
    fi \
    && chown "${USER_UID}:${USER_GID}" "${BASHRC}"

WORKDIR /home/${USERNAME}
USER ${USERNAME}
ENTRYPOINT ["/entrypoint.sh"]
CMD ["/bin/bash"]
