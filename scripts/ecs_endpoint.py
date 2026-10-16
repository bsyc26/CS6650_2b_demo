#!/usr/bin/env python3
"""Resolve this Terraform service's running task and public API URL through AWS CLI."""

import json
import os
from pathlib import Path
import subprocess
import sys


def main():
    root = Path(__file__).resolve().parents[1]
    env = dict(os.environ)
    env.setdefault("AWS_PROFILE", "learner-lab")
    env["AWS_PAGER"] = ""
    outputs = json.loads(subprocess.check_output(
        ["terraform", "-chdir=" + str(root / "terraform"), "output", "-json"], env=env, text=True))
    region = outputs["aws_region"]["value"]
    cluster = outputs["ecs_cluster_name"]["value"]
    service = outputs["ecs_service_name"]["value"]

    def aws(*arguments):
        command = ["aws", "--region", region, "--output", "json", *arguments]
        return json.loads(subprocess.check_output(command, env=env, text=True))

    tasks = aws("ecs", "list-tasks", "--cluster", cluster, "--service-name", service, "--desired-status", "RUNNING")["taskArns"]
    if len(tasks) != 1:
        raise RuntimeError(f"Expected one running task, found {len(tasks)}; inspect the ECS service events")
    reply = aws("ecs", "describe-tasks", "--cluster", cluster, "--tasks", tasks[0])
    if reply.get("failures") or len(reply.get("tasks", [])) != 1:
        raise RuntimeError("AWS could not describe the selected task")
    task = reply["tasks"][0]
    if task["lastStatus"] != "RUNNING":
        raise RuntimeError("Task is not RUNNING yet")
    interfaces = [item["value"] for attachment in task.get("attachments", [])
                  if attachment.get("type") == "ElasticNetworkInterface"
                  for item in attachment.get("details", []) if item["name"] == "networkInterfaceId"]
    if len(interfaces) != 1:
        raise RuntimeError("Task does not yet have one network interface")
    interface = aws("ec2", "describe-network-interfaces", "--network-interface-ids", interfaces[0])["NetworkInterfaces"][0]
    address = interface.get("Association", {}).get("PublicIp")
    if not address:
        raise RuntimeError("Task network interface has no public IPv4 address")
    port = outputs["deployment_configuration"]["value"]["port"]
    print(f"http://{address}:{port}/v1")


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, KeyError, subprocess.CalledProcessError) as error:
        print(f"Could not resolve Product API endpoint: {error}", file=sys.stderr)
        sys.exit(1)
