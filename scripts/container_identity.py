"""Image identity shared by runtime imports, OCI archives and publication."""

PLATFORMS = {"linux-amd64": ("amd64", ""), "linux-arm64": ("arm64", ""), "linux-armv7": ("arm", "v7")}


def verify_image(info, role, platform, repository, source_sha, version):
    config = info["Config"]
    labels = config.get("Labels") or {}
    expected_arch, expected_variant = PLATFORMS[platform]
    if info["Os"] != "linux" or info["Architecture"] != expected_arch:
        raise ValueError("image platform does not match its tested artifact")
    if expected_variant and info.get("Variant") != expected_variant:
        raise ValueError("image ARM variant does not match its tested artifact")
    if config["User"] != "65532:65532" or config["Entrypoint"] != ["/usr/local/bin/entropy-" + role]:
        raise ValueError("unexpected image identity or entrypoint")
    if labels.get("org.opencontainers.image.revision") != source_sha:
        raise ValueError("image source differs from the validated source commit")
    if labels.get("org.opencontainers.image.version") != version:
        raise ValueError("image version differs from the requested channel/release")
    if labels.get("org.opencontainers.image.source", "").lower() != "https://github.com/" + repository.lower():
        raise ValueError("image is not linked to the requested source repository")


