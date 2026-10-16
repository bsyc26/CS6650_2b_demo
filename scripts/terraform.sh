#!/usr/bin/env bash
set -euo pipefail
# Terraform state and saved plans can contain runtime credentials.
umask 077
hw5_root=$(cd "$(dirname "$0")/.." && pwd)
export AWS_PROFILE="${AWS_PROFILE:-learner-lab}"
export AWS_REGION="${TF_VAR_aws_region:-${AWS_REGION:-us-east-1}}"
export TF_VAR_aws_region="$AWS_REGION"
export CHECKPOINT_DISABLE=1

if [[ -f "$hw5_root/.env.aws" ]]; then
  # This ignored file is generated locally by prepare_cloud.py.
  source "$hw5_root/.env.aws"
  export TF_VAR_product_api_key="$PRODUCT_API_KEY"
fi

if [[ -z "${DOCKER_HOST:-}" ]]; then
  hw5_docker=$(command -v docker || true)
  if [[ -z "$hw5_docker" && -x /Applications/Docker.app/Contents/Resources/bin/docker ]]; then
    hw5_docker=/Applications/Docker.app/Contents/Resources/bin/docker
  fi
  if [[ -n "$hw5_docker" ]]; then
    export DOCKER_HOST
    DOCKER_HOST=$("$hw5_docker" context inspect --format '{{.Endpoints.docker.Host}}')
  fi
fi

if [[ -d /Applications/Docker.app/Contents/Resources/bin ]]; then
  export PATH="/Applications/Docker.app/Contents/Resources/bin:$PATH"
fi

exec terraform -chdir="$hw5_root/terraform" "$@"
