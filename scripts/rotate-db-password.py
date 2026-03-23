#!/usr/bin/env python3
"""
Rotate the RDS database password.
Updates SSM, Secrets Manager, all Lambda env vars, and k8s external secrets.

Usage:
    python3 scripts/rotate-db-password.py
"""

import boto3
import json
import secrets
import string
import time

REGION = "eu-west-1"
DB_INSTANCE = "database-1"
SSM_PARAM = "/mirrorfm/db/password"
SECRET_NAMES = [
    "homeplane/from-youtube",
    "homeplane/from-discogs",
    "homeplane/to-spotify",
    "homeplane/manage-playlists",
]
LAMBDA_FUNCTIONS = [
    "mirror-fm_to-www",
    "mirror-fm_from-github",
    "mirror-fm_from-youtube",
    "mirror-fm_from-discogs",
    "mirror-fm_to-spotify",
    "mirror-fm_manage-playlists",
]

rds = boto3.client("rds", region_name=REGION)
ssm = boto3.client("ssm", region_name=REGION)
sm = boto3.client("secretsmanager", region_name=REGION)
lm = boto3.client("lambda", region_name=REGION)

# Generate new password
new_pass = "".join(secrets.choice(string.ascii_letters + string.digits) for _ in range(24))

# 1. Update RDS
rds.modify_db_instance(
    DBInstanceIdentifier=DB_INSTANCE,
    MasterUserPassword=new_pass,
    ApplyImmediately=True,
)
print("1/4 RDS password updated")

# 2. Update SSM
ssm.put_parameter(Name=SSM_PARAM, Value=new_pass, Type="SecureString", Overwrite=True)
print("2/4 SSM parameter updated")

# 3. Update Secrets Manager
for name in SECRET_NAMES:
    secret = json.loads(sm.get_secret_value(SecretId=name)["SecretString"])
    secret["DB_PASSWORD"] = new_pass
    sm.put_secret_value(SecretId=name, SecretString=json.dumps(secret))
    print(f"     Updated {name}")
print("3/4 Secrets Manager updated")

# 4. Update Lambda env vars
for func in LAMBDA_FUNCTIONS:
    try:
        config = lm.get_function_configuration(FunctionName=func)
        env_vars = config.get("Environment", {}).get("Variables", {})
        env_vars["DB_PASSWORD"] = new_pass
        lm.update_function_configuration(
            FunctionName=func, Environment={"Variables": env_vars}
        )
        print(f"     Updated {func}")
    except Exception as e:
        print(f"     Failed {func}: {e}")
print("4/4 Lambda env vars updated")

print("\nDone. Resync k8s external secrets and restart pods:")
print("  kubectl annotate externalsecret -n mirrorfm --all force-sync=$(date +%s) --overwrite")
print("  kubectl rollout restart deployment -n mirrorfm")
