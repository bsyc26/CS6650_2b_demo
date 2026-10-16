#!/usr/bin/env python3
"""Use curl to exercise the Product response codes against a running server.

PRODUCT_API_KEY=hw5-local-key python3 scripts/check_api.py --faults
Use --faults only with a server explicitly started with PRODUCT_TEST_FAULTS=true.
"""

import argparse
import json
import os
import subprocess
import sys


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", default="http://127.0.0.1:8080/v1")
    parser.add_argument("--faults", action="store_true", help="also test the opt-in 500 responses")
    args = parser.parse_args()
    key = os.environ.get("PRODUCT_API_KEY", "")
    if not key:
        parser.error("set PRODUCT_API_KEY to the running server's API key")

    def call(label, method, path, expected, body=None, *, raw=None, headers=None):
        request_headers = {"X-API-Key": key, "Content-Type": "application/json"}
        request_headers.update(headers or {})
        data = raw if raw is not None else None if body is None else json.dumps(body).encode()
        command = ["curl", "--silent", "--show-error", "--max-time", "10", "--request", method,
                   "--write-out", "\n%{http_code}\n%{content_type}"]
        for name, value in request_headers.items():
            command += ["--header", name + ": " + value]
        if data is not None:
            command += ["--data-binary", "@-"]
        command += [args.base_url.rstrip("/") + path]
        result = subprocess.run(command, input=data, capture_output=True)
        if result.returncode:
            raise OSError("curl failed: " + result.stderr.decode(errors="replace"))
        content, raw_status, content_type = result.stdout.rsplit(b"\n", 2)
        status = int(raw_status)
        assert status == expected, f"{label}: expected {expected}, got {status}: {content!r}"
        if status == 204:
            assert content == b"", f"{label}: 204 must have an empty body"
            decoded = None
        else:
            assert content_type.split(b";", 1)[0] == b"application/json"
            decoded = json.loads(content)
            assert isinstance(decoded, dict)
            if status >= 400:
                assert isinstance(decoded.get("error"), str) and decoded["error"]
                assert isinstance(decoded.get("message"), str) and decoded["message"]
            elif status == 200:
                validate_product(decoded)
        print(f"PASS {method:4} {path:30} -> {status}  {label}", flush=True)
        if decoded is not None:
            print("     " + json.dumps(decoded, ensure_ascii=False), flush=True)
        return decoded

    original = call("existing product", "GET", "/products/12345", 200)
    updated = dict(original, sku="HW5-SMOKE-UPDATED", manufacturer="Local test maker", weight=0)
    try:
        call("successful update, empty response", "POST", "/products/12345/details", 204, updated)
        actual = call("read your write", "GET", "/products/12345", 200)
        assert actual == updated, "GET does not match the successful POST"
        call("missing product", "GET", "/products/2147483647", 404)
        call("invalid JSON", "POST", "/products/12345/details", 400, raw=b"{")
        for field in original:
            incomplete = dict(original)
            del incomplete[field]
            call("missing required " + field, "POST", "/products/12345/details", 400, incomplete)
        call("mismatched URL/body ID", "POST", "/products/12345/details", 400, dict(original, product_id=2))
        call("negative weight", "POST", "/products/12345/details", 400, dict(original, weight=-1))
        call("int32 overflow", "POST", "/products/12345/details", 400, dict(original, weight=2147483648))
        call("SKU too long", "POST", "/products/12345/details", 400, dict(original, sku="x" * 101))
        call("duplicate SKU", "POST", "/products/12345/details", 400, dict(original, sku="HW5-001"))
        call("unknown product cannot be created", "POST", "/products/2147483647/details", 404,
             dict(original, product_id=2147483647, sku="MISSING"))
        call("invalid GET ID", "GET", "/products/0", 404)
        call("invalid POST ID", "POST", "/products/0/details", 400, original)
        call("authentication required", "GET", "/products/12345", 401, headers={"X-API-Key": ""})
        if args.faults:
            failure = {"X-HW5-Test-Failure": "panic"}
            call("controlled recovery path", "GET", "/products/12345", 500, headers=failure)
            call("controlled recovery path", "POST", "/products/12345/details", 500, original, headers=failure)
            call("server remains healthy after errors", "GET", "/products/12345", 200)
    finally:
        call("restore original product", "POST", "/products/12345/details", 204, original)
    print("All selected HTTP checks passed. " + ("All 7 declared Product response paths covered." if args.faults else "500 demonstration skipped; rerun with --faults on an opted-in test server."))


def validate_product(product):
    # The public Product constraints from api.yaml; bool is not a JSON integer here.
    for field in ("product_id", "category_id", "some_other_id", "weight"):
        value = product[field]
        minimum = 0 if field == "weight" else 1
        assert type(value) is int and minimum <= value <= 2147483647, (field, value)
    for field, limit in (("sku", 100), ("manufacturer", 200)):
        assert isinstance(product[field], str) and 1 <= len(product[field]) <= limit


if __name__ == "__main__":
    try:
        main()
    except (AssertionError, OSError, ValueError, KeyError) as error:
        print(f"FAIL: {error}", file=sys.stderr)
        sys.exit(1)
