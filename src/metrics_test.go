package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestREDRecordsFinalStatusAndExcludesScrapes(t *testing.T) {
	m := newRequestMetrics()
	r := newRouter(newMemoryStore(), serverOptions{APIKey: testKey, Metrics: m, TestFaults: true})
	for _, id := range []string{"1", "2", "9999"} {
		request(r, "GET", "/v1/products/"+id, "")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/v1/products/1", nil)) // 401
	req := httptest.NewRequest("GET", "/v1/products/1", nil)
	req.Header.Set("X-API-Key", testKey)
	req.Header.Set("X-HW5-Test-Failure", "panic")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req) // Recovered 500 must be counted, not a provisional 200.
	request(r, "POST", "/v1/products/1/details", productJSON(1, "NEW", 0))
	request(r, "POST", "/v1/products/1/details", "{")
	request(r, "GET", "/unknown/12345", "")
	for i := 0; i < 2; i++ {
		w = request(r, "GET", "/metrics", "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), "product_http_request_duration_seconds_bucket") {
			t.Fatal("metrics are not exposed in Prometheus format")
		}
	}
	families, err := m.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	counts, errors, observations, statuses := map[string]float64{}, map[string]float64{}, map[string]uint64{}, map[string]float64{}
	for _, family := range families {
		if !strings.HasPrefix(family.GetName(), "product_http_") {
			continue
		}
		for _, metric := range family.Metric {
			labels := map[string]string{}
			for _, label := range metric.Label {
				labels[label.GetName()] = label.GetValue()
			}
			endpoint := labels["endpoint"]
			if endpoint == "/metrics" || strings.Contains(endpoint, "9999") || strings.Contains(endpoint, "12345") {
				t.Fatalf("scrape or unbounded path leaked into labels: %v", labels)
			}
			key := labels["method"] + " " + endpoint
			switch family.GetName() {
			case "product_http_requests_total":
				counts[key] += metric.GetCounter().GetValue()
				statuses[key+" "+labels["status"]] = metric.GetCounter().GetValue()
			case "product_http_errors_total":
				errors[key] = metric.GetCounter().GetValue()
			case "product_http_request_duration_seconds":
				observations[key] = metric.GetHistogram().GetSampleCount()
			}
		}
	}
	get, post := "GET /v1/products/:productId", "POST /v1/products/:productId/details"
	if counts[get] != 5 || counts[post] != 2 || errors[get] != 3 || errors[post] != 1 {
		t.Fatalf("wrong RED counters: counts=%v errors=%v", counts, errors)
	}
	if observations[get] != 5 || observations[post] != 2 || counts["GET unmatched"] != 1 {
		t.Fatalf("wrong histogram counts or fallback label: %v %v", observations, counts)
	}
	if statuses[get+" 500"] != 1 || statuses[get+" 401"] != 1 || statuses[post+" 204"] != 1 {
		t.Fatalf("final response status was not recorded: %v", statuses)
	}
}

func TestREDRegistriesAreIndependent(t *testing.T) {
	first, second := newRequestMetrics(), newRequestMetrics()
	r := newRouter(newMemoryStore(), serverOptions{APIKey: testKey, Metrics: first})
	request(r, "GET", "/v1/products/1", "")
	other := newRouter(newMemoryStore(), serverOptions{APIKey: testKey, Metrics: second})
	w := request(other, "GET", "/metrics", "")
	if !strings.Contains(w.Body.String(), `product_http_requests_total{endpoint="/v1/products/:productId",method="GET",status="200"} 0`) {
		t.Fatal("independent server registry inherited another server's requests")
	}
}
