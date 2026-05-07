"""Topic naming helpers for PX4 multi-instance ROS setups."""


def instance_to_prefix(instance: int) -> str:
    """Convert PX4 instance id to ROS prefix.

    Instance 0 maps to the unprefixed namespace and instance N to `/px4_N`.
    """
    return "" if instance == 0 else f"/px4_{instance}"


def topic_with_prefix(prefix: str, topic: str) -> str:
    """Return a fully qualified topic with optional instance prefix."""
    normalized_topic = topic.strip()
    if not normalized_topic:
        raise ValueError("Topic name cannot be empty")
    if not normalized_topic.startswith("/"):
        normalized_topic = "/" + normalized_topic

    normalized_prefix = prefix.strip()
    if not normalized_prefix:
        return normalized_topic
    if not normalized_prefix.startswith("/"):
        normalized_prefix = "/" + normalized_prefix
    return f"{normalized_prefix}{normalized_topic}"
