"""Inventory SKU HTTP and v1.2.111 upgrade acceptance in disposable containers.

ZCARD_TEST_IMAGE=zcard-sku-api:dev python3 scripts/test_inventory_sku_api.py
Only newly created containers and a random volume are modified and removed.
"""
import json
import os
import secrets
import subprocess
import time
import urllib.error
import urllib.request
import uuid


def docker(*args):
    return subprocess.check_output(["docker", *args], text=True).strip()


def main():
    image = os.environ.get("ZCARD_TEST_IMAGE", "zcard-sku-api:dev")
    old_image = os.environ.get("ZCARD_TEST_OLD_IMAGE", "nealbaker/zcard-next:v1.2.111")
    name = "zcard-sku-api-" + uuid.uuid4().hex[:12]
    volume = name + "-data"
    docker("volume", "create", volume)
    token, base = "", ""
    password = secrets.token_urlsafe(24)

    def request(path, body=None, method=None, auth=None, status=200):
        headers = {"Content-Type": "application/json"}
        if auth or token:
            headers["Authorization"] = "Bearer " + (auth or token)
        req = urllib.request.Request(base + path, data=json.dumps(body).encode() if body is not None else None,
                                     headers=headers, method=method)
        try:
            with urllib.request.urlopen(req, timeout=10) as response:
                code, raw = response.status, response.read()
        except urllib.error.HTTPError as error:
            code, raw = error.code, error.read()
        value = json.loads(raw) if raw else {}
        assert code == status, (path, code, value.get("reason"), value.get("message"))
        return value

    def start(which):
        nonlocal base
        docker("run", "-d", "--name", name, "-p", "127.0.0.1::8000", "-v", volume + ":/app", which)
        base = "http://" + docker("port", name, "8000").splitlines()[0]
        for _ in range(120):
            try:
                if request("/health")["status"]["database"]:
                    return
            except (OSError, ValueError, AssertionError):
                pass
            time.sleep(0.5)
        raise AssertionError("isolated server did not become healthy")

    try:
        start(old_image)
        request("/api/v1/admin/install", {"admin_username": "sku-admin", "admin_password": password,
                                          "site_name": "SKU acceptance", "site_url": base, "dialect": "sqlite"})
        token = request("/api/v1/admin/auth/login", {"username": "sku-admin", "password": password})["access_token"]
        p = request("/api/v1/admin/products", {"name": "SKU membership", "slug": "sku-membership", "price_cents": 100,
                                              "stock_type": "card", "status": 1, "stock_visible": True})
        pid = p["id"]
        old_batch = request("/api/v1/admin/inventory/import", {"product_id": pid, "lines": ["OLD-DEFAULT-A", "OLD-DEFAULT-B"]})
        skus = [request(f"/api/v1/admin/products/{pid}/skus", {"name": term, "spec_values": {"term": term}, "price_cents": 100})
                for term in ("monthly", "yearly")]
        monthly, yearly = (sku["id"] for sku in skus)
        docker("rm", "-f", name)
        start(image)
        # JWT keys and users survive the upgrade; authenticate again to exercise it.
        token = request("/api/v1/admin/auth/login", {"username": "sku-admin", "password": password})["access_token"]
        targets = request(f"/api/v1/admin/inventory/targets?product_id={pid}")
        assert targets["has_skus"] and len(targets["targets"]) == 3
        assert next(t for t in targets["targets"] if t.get("sku_id", 0) == 0).get("available") == 2
        legacy = request(f"/api/v1/admin/inventory/imports?product_id={pid}&legacy_only=true")["batches"]
        assert len(legacy) == 1 and legacy[0]["id"] == old_batch["id"] and "sku_id" not in legacy[0]
        missing = request("/api/v1/admin/inventory/import", {"product_id": pid, "lines": ["MISSING-TARGET"]}, status=400)
        assert missing["reason"] == "inventory.SKU_REQUIRED"
        marker = "HTTP-SKU-PRIVATE-CARD-"
        for sku, lines in ((monthly, [marker + "MONTH-A", marker + "MONTH-B"]), (yearly, [marker + "YEAR-A"])):
            payload = {"product_id": pid, "sku_id": sku, "lines": lines}
            pre = request("/api/v1/admin/inventory/preview", payload)
            payload["expected_revision"] = pre.get("revision", 0)
            batch = request("/api/v1/admin/inventory/import", payload)
            assert batch["sku_id"] == sku and batch["imported"] == len(lines)
        count = lambda query: request(f"/api/v1/admin/inventory/cards?product_id={pid}" + query).get("total", 0)
        assert count("") == 5 and count("&sku_id=0") == 2 and count(f"&sku_id={monthly}") == 2
        exported = request("/api/v1/admin/inventory/export", {"product_id": pid, "sku_id": yearly})
        assert exported["lines"] == [marker + "YEAR-A"]
        # Split an old batch, preserving its original unknown target.
        pre = request("/api/v1/admin/inventory/assign-preview", {"product_id": pid, "sku_id": monthly, "import_id": old_batch["id"]})
        assert pre["eligible_count"] == 2
        request("/api/v1/admin/inventory/assign", {"product_id": pid, "sku_id": monthly, "card_ids": [pre["card_ids"][0]],
                                                  "expected_revision": pre.get("revision", 0), "request_key": "http-acceptance"})
        assert count("&sku_id=0") == 1 and count(f"&sku_id={monthly}") == 3
        preview_path = f"/api/v1/admin/inventory/imports/{old_batch['id']}/preview"
        cancel = request(preview_path)
        assert len(cancel["batch"]["remaining"]) == 2 and "sku_id" not in cancel["batch"]
        request(f"/api/v1/admin/inventory/imports/{old_batch['id']}/cancel", {"expected_revision": cancel["revision"]})
        assert count("") == 3
        # Read-only inventory roles can obtain names/targets but cannot import or
        # read the catalog SKU DTO containing costs.
        role = request("/api/v1/admin/authz/roles", {"name": "Inventory reader", "code": "sku_reader",
                                                    "permissions": ["auth:profile", "inventory:read"]})
        reader_password = secrets.token_urlsafe(24)
        request("/api/v1/admin/admins", {"username": "sku-reader", "password": reader_password, "role_id": role["id"]})
        reader = request("/api/v1/admin/auth/login", {"username": "sku-reader", "password": reader_password})["access_token"]
        visible = request(f"/api/v1/admin/inventory/targets?product_id={pid}", auth=reader)
        assert "cost_cents" not in json.dumps(visible) and "price_cents" not in json.dumps(visible)
        request(f"/api/v1/admin/products/{pid}/skus", auth=reader, status=403)
        request("/api/v1/admin/inventory/import", {"product_id": pid, "sku_id": monthly, "lines": ["FORBIDDEN"]}, auth=reader, status=403)
        # The new secret marker must never reach ordinary operation audit, even
        # though old-version requests deliberately preceded this upgrade.
        time.sleep(0.2)
        logs = request("/api/v1/admin/audit/op-logs?page=1&page_size=100")
        assert marker not in json.dumps(logs), "new import card content leaked into audit"
        docker("rm", "-f", name)
        start(image)
        assert count("") == 3 and count(f"&sku_id={monthly}") == 2
        print("PASS: v1.2.111 SQLite upgrade, unknown batch provenance, HTTP SKU presence, import/filter/export, split assignment and whole-batch cancellation, reader RBAC, audit redaction, persisted settings")
    finally:
        subprocess.run(["docker", "rm", "-f", name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        subprocess.run(["docker", "volume", "rm", volume], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


if __name__ == "__main__":
    main()
