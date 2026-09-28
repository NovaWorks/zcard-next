#!/usr/bin/env python3
"""Keep marketplace server schemas out of the public instance repository."""
import subprocess
import sys

PREFIX = "server/internal/data/ent/schema/"
PRIVATE = {name + suffix + ".go" for name in (
    "tenant", "escrow", "subscription", "api_key", "api_scope",
    "plugin_manifest", "market_supplier", "supplier_rating", "market_plugin",
    "market_version", "market_artifact", "market_catalog", "market_audit",
) for suffix in ("", "s")} | {"licenses.go"}


def forbidden(path):
    parts = path.split("/")
    return (path.startswith("market/") or "saas" in parts or "internal/commercial/" in path
            or path.startswith(PREFIX) and path[len(PREFIX):] in PRIVATE)


def self_test():
    public = ["license", "installed_plugin", "plugin_data", "plugin_requirement",
              "plugin_operation", "plugin_rule_level_ref"]
    for name in public:
        assert not forbidden(PREFIX + name + ".go"), name
    for name in PRIVATE:
        assert forbidden(PREFIX + name), name
    for name in ("market/go.mod", "market/web/index.html", "server/internal/commercial/x.go", "saas/x.go", "server/saas/x.go"):
        assert forbidden(name), name
    assert not forbidden("docs/licenses.go")


if __name__ == "__main__":
    self_test()
    if "--self-test" not in sys.argv:
        files = subprocess.check_output(["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z"]).decode().split("\0")
        bad = [p for p in files if forbidden(p)]
        if bad:
            sys.exit("Private marketplace files in public tree:\n" + "\n".join(bad))
    print("open-source guard passed")
