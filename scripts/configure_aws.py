#!/usr/bin/env python3
"""Interactively save Learner Lab credentials without printing or shell-history arguments."""

import argparse
import configparser
import getpass
import os
from pathlib import Path
import sys
import tempfile


def write_config(path, section, values):
    config = configparser.RawConfigParser()
    if path.exists():
        config.read(path)
    if not config.has_section(section):
        config.add_section(section)
    for key, value in values.items():
        config.set(section, key, value)
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(prefix=".hw5-", dir=path.parent)
    try:
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, "w") as output:
            config.write(output)
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--profile", default="learner-lab")
    parser.add_argument("--region", default="us-east-1")
    args = parser.parse_args()
    if not sys.stdin.isatty():
        parser.error("run this script in your own interactive terminal; all three inputs are hidden")
    if not args.profile.strip() or any(x in args.profile for x in "\r\n[]"):
        parser.error("profile must be a nonempty AWS profile name")
    print("Start Learner Lab, then copy the three values from AWS Details.")
    values = {}
    for key in ("aws_access_key_id", "aws_secret_access_key", "aws_session_token"):
        value = getpass.getpass(key + " (hidden): ").strip()
        if not value or "\n" in value or "\r" in value:
            parser.error("each credential must be a nonempty single-line value; nothing was saved")
        values[key] = value
    credential_file = Path(os.environ.get("AWS_SHARED_CREDENTIALS_FILE", Path.home() / ".aws/credentials")).expanduser()
    config_file = Path(os.environ.get("AWS_CONFIG_FILE", Path.home() / ".aws/config")).expanduser()
    write_config(credential_file, args.profile, values)
    section = "default" if args.profile == "default" else "profile " + args.profile
    write_config(config_file, section, {"region": args.region, "output": "json"})
    print(f"Saved profile {args.profile} for {args.region}. Credential values were not printed.")
    print(f"Verify with: AWS_PROFILE={args.profile} aws sts get-caller-identity")


if __name__ == "__main__":
    main()
