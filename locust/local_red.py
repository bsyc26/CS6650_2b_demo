"""Local dashboard demonstration: 80% GET, 18% POST, 1% 404, 1% 400.

Expected 4xx responses are successful Locust test cases but remain errors in the
server's RED definition (all non-2xx). No panic/fault injection is enabled.
"""

from locust import FastHttpUser, TaskSet, constant, task
from locustfile import API_KEY, PRODUCT, ProductTasks


class LocalREDTasks(TaskSet):
    @task(80)
    def get_product(self):
        ProductTasks.get_product(self)

    @task(18)
    def update_product(self):
        ProductTasks.update_product(self)

    @task(1)
    def missing_product(self):
        with self.client.get(
            "/v1/products/2147483647", headers={"X-API-Key": API_KEY},
            name="/v1/products/{productId} [expected 404]", catch_response=True,
        ) as response:
            if response.status_code == 0:
                return
            if response.status_code == 404:
                response.success()
            else:
                response.failure(f"Expected 404, got {response.status_code}")

    @task(1)
    def invalid_details(self):
        with self.client.post(
            "/v1/products/12345/details", headers={"X-API-Key": API_KEY},
            json=dict(PRODUCT, weight=-1),
            name="/v1/products/{productId}/details [expected 400]", catch_response=True,
        ) as response:
            if response.status_code == 0:
                return
            if response.status_code == 400:
                response.success()
            else:
                response.failure(f"Expected 400, got {response.status_code}")


class ProductREDFastUser(FastHttpUser):
    tasks = [LocalREDTasks]
    wait_time = constant(0)
    connection_timeout = 5
    network_timeout = 10
    request_options = {}
