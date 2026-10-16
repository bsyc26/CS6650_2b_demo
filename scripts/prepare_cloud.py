#!/usr/bin/env python3
"""Create ignored local application credentials and the caller's IPv4 CIDR for Terraform."""

import argparse
import ipaddress
import json
import os
from pathlib import Path
import secrets
from urllib.request import urlopen


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--refresh-network", action="store_true", help="update the generated CIDR after a network change")
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    env_file = root / ".env.aws"
    if not env_file.exists():
        fd = os.open(env_file, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "w") as output:
            output.write("PRODUCT_API_KEY=" + secrets.token_urlsafe(32) + "\n")
        print("Created ignored .env.aws with a new application API key; its value is not displayed.")
    else:
        print("Kept the existing application API key.")
    config_file = root / "terraform/client.auto.tfvars.json"
    if config_file.exists() and not args.refresh_network:
        print("Kept the existing client CIDR; use --refresh-network after changing networks.")
        return
    with urlopen("https://checkip.amazonaws.com", timeout=15) as response:
        address = ipaddress.IPv4Address(response.read().decode().strip())
    config = {"allowed_cidrs": [str(address) + "/32"]}
    fd = os.open(config_file, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w") as output:
        json.dump(config, output, indent=2)
        output.write("\n")
    print("Saved the current public IPv4 /32 in ignored terraform/client.auto.tfvars.json.")


if __name__ == "__main__":
    main()
