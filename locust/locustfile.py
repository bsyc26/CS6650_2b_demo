"""The same Product workload for HttpUser and FastHttpUser.

Run exactly one user class per comparison. No deliberate error requests or fault
injection are mixed into the capacity measurement.
"""

import os

from locust import FastHttpUser, HttpUser, TaskSet, between, task


API_KEY = os.environ.get("PRODUCT_API_KEY", "")
if not API_KEY:
    raise ValueError("Set PRODUCT_API_KEY to the deployed service key")

WAIT_MIN = float(os.environ.get("WAIT_MIN", "0.1"))
WAIT_MAX = float(os.environ.get("WAIT_MAX", "0.3"))
if not 0 <= WAIT_MIN <= WAIT_MAX:
    raise ValueError("Require 0 <= WAIT_MIN <= WAIT_MAX")

# Repeat a valid replacement of the seeded item. This exercises locking and SKU
# indexing without creating duplicate SKUs or leaving altered data after a run.
PRODUCT = {
    "product_id": 12345,
    "sku": "ABC-123-XYZ",
    "manufacturer": "Acme Corporation",
    "category_id": 456,
    "weight": 1250,
    "some_other_id": 789,
}


class ProductTasks(TaskSet):
    @task(4)
    def get_product(self):
        with self.client.get(
            "/v1/products/12345",
            name="/v1/products/{productId}",
            headers={"X-API-Key": API_KEY},
            catch_response=True,
            **self.user.request_options,
        ) as response:
            if response.status_code == 0:
                # Keep Locust's original connection/timeout exception details.
                return
            if response.status_code != 200:
                response.failure(f"Expected 200, got {response.status_code}")
                return
            try:
                actual = response.json()
            except ValueError:
                response.failure("GET returned invalid JSON")
                return
            if actual != PRODUCT:
                response.failure("GET Product body differs from the seeded workload")

    @task(1)
    def update_product(self):
        with self.client.post(
            "/v1/products/12345/details",
            name="/v1/products/{productId}/details",
            headers={"X-API-Key": API_KEY},
            json=PRODUCT,
            catch_response=True,
            **self.user.request_options,
        ) as response:
            if response.status_code == 0:
                return
            if response.status_code != 204 or response.content:
                response.failure(f"Expected empty 204, got {response.status_code}")


class ProductHttpUser(HttpUser):
    tasks = [ProductTasks]
    wait_time = between(WAIT_MIN, WAIT_MAX)
    request_options = {"timeout": (5, 10)}


class ProductFastHttpUser(FastHttpUser):
    tasks = [ProductTasks]
    wait_time = between(WAIT_MIN, WAIT_MAX)
    connection_timeout = 5
    network_timeout = 10
    request_options = {}
