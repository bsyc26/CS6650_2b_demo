package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

const testKey = "local-test-key"

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

func request(r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("X-API-Key", testKey)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func productJSON(id int32, sku string, weight int32) string {
	return fmt.Sprintf(`{"product_id":%d,"sku":%q,"manufacturer":"Test Maker","category_id":1,"weight":%d,"some_other_id":1}`, id, sku, weight)
}

func assertError(t *testing.T, w *httptest.ResponseRecorder, status int) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, status, w.Body.String())
	}
	var e APIError
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil || e.Error == "" || e.Message == "" {
		t.Fatalf("response does not match Error schema: %s", w.Body.String())
	}
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Fatal("error response must be JSON")
	}
}

func TestProductLifecycle(t *testing.T) {
	r := newRouter(newMemoryStore(), serverOptions{APIKey: testKey})
	w := request(r, "GET", "/v1/products/12345", "")
	if w.Code != 200 {
		t.Fatalf("GET seed: %d", w.Code)
	}
	var got Product
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.SKU != "ABC-123-XYZ" {
		t.Fatalf("unexpected seed response: %s", w.Body.String())
	}
	w = request(r, "POST", "/v1/products/12345/details", productJSON(12345, "UPDATED", 0))
	if w.Code != 204 || w.Body.Len() != 0 {
		t.Fatalf("POST must return empty 204: %d %s", w.Code, w.Body.String())
	}
	w = request(r, "GET", "/products/12345", "") // Both prefixes use the same store.
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != 200 || got.SKU != "UPDATED" || got.Weight != 0 {
		t.Fatalf("update did not persist across routes: %s", w.Body.String())
	}
	assertError(t, request(r, "GET", "/v1/products/999999", ""), 404)
	assertError(t, request(r, "POST", "/v1/products/999999/details", productJSON(999999, "MISSING", 1)), 404)
	assertError(t, request(r, "GET", "/v1/products/999999", ""), 404) // POST must not create.
}

func TestValidation(t *testing.T) {
	valid := productJSON(1, "NEW-SKU", 0)
	cases := []struct{ name, path, body string }{
		{"malformed JSON", "1", "{"},
		{"empty body", "1", ""},
		{"null body", "1", "null"},
		{"array body", "1", "[]"},
		{"two JSON objects", "1", valid + valid},
		{"trailing garbage", "1", valid + "x"},
		{"mismatched ID", "2", valid},
		{"negative path", "-1", valid},
		{"zero path", "0", valid},
		{"nonnumeric path", "abc", valid},
		{"overflow path", "2147483648", valid},
		{"zero product ID", "1", strings.Replace(valid, `"product_id":1`, `"product_id":0`, 1)},
		{"negative weight", "1", productJSON(1, "NEW", -1)},
		{"overflow body", "1", strings.Replace(valid, `"weight":0`, `"weight":2147483648`, 1)},
		{"fractional integer", "1", strings.Replace(valid, `"weight":0`, `"weight":1.5`, 1)},
		{"wrong field type", "1", strings.Replace(valid, `"weight":0`, `"weight":"0"`, 1)},
		{"empty SKU", "1", productJSON(1, "", 1)},
		{"long SKU", "1", productJSON(1, strings.Repeat("界", 101), 1)},
		{"empty manufacturer", "1", strings.Replace(valid, "Test Maker", "", 1)},
		{"long manufacturer", "1", strings.Replace(valid, "Test Maker", strings.Repeat("界", 201), 1)},
		{"zero category", "1", strings.Replace(valid, `"category_id":1`, `"category_id":0`, 1)},
		{"negative other ID", "1", strings.Replace(valid, `"some_other_id":1`, `"some_other_id":-1`, 1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRouter(newMemoryStore(), serverOptions{APIKey: testKey})
			assertError(t, request(r, "POST", "/v1/products/"+tc.path+"/details", tc.body), 400)
		})
	}
	for _, field := range []string{"product_id", "sku", "manufacturer", "category_id", "weight", "some_other_id"} {
		for _, null := range []bool{false, true} {
			t.Run(fmt.Sprintf("required %s null=%v", field, null), func(t *testing.T) {
				var body map[string]any
				json.Unmarshal([]byte(valid), &body)
				if null {
					body[field] = nil
				} else {
					delete(body, field)
				}
				data, _ := json.Marshal(body)
				r := newRouter(newMemoryStore(), serverOptions{APIKey: testKey})
				assertError(t, request(r, "POST", "/v1/products/1/details", string(data)), 400)
			})
		}
	}
}

func TestSchemaBoundariesAndAdditionalProperties(t *testing.T) {
	r := newRouter(newMemoryStore(), serverOptions{APIKey: testKey})
	body := productJSON(1, strings.Repeat("界", 100), 2147483647)
	body = strings.Replace(body, "Test Maker", strings.Repeat("界", 200), 1)
	body = strings.TrimSuffix(body, "}") + `,"extra":"allowed by the OpenAPI schema"}`
	w := request(r, "POST", "/v1/products/1/details", body)
	if w.Code != 204 {
		t.Fatalf("valid boundary values rejected: %s", w.Body.String())
	}
	for _, id := range []string{"0", "-1", "abc", "2147483648"} {
		assertError(t, request(r, "GET", "/v1/products/"+id, ""), 404)
	}
	req := httptest.NewRequest("POST", "/v1/products/1/details", strings.NewReader(productJSON(1, "NEW", 0)))
	req.Header.Set("X-API-Key", testKey)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assertError(t, w, 400)
	w = request(r, "POST", "/v1/products/1/details", `{"padding":"`+strings.Repeat("x", 1<<20)+`"}`)
	assertError(t, w, 400)
}

func TestSKUUniquenessAndAtomicFailure(t *testing.T) {
	r := newRouter(newMemoryStore(), serverOptions{APIKey: testKey})
	assertError(t, request(r, "POST", "/v1/products/1/details", productJSON(1, "HW5-002", 99)), 400)
	w := request(r, "GET", "/v1/products/1", "")
	var p Product
	json.Unmarshal(w.Body.Bytes(), &p)
	if p.SKU != "HW5-001" || p.Weight != 250 {
		t.Fatal("rejected update must not change stored data")
	}
	if w = request(r, "POST", "/v1/products/1/details", productJSON(1, "NEW", 1)); w.Code != 204 {
		t.Fatal(w.Body.String())
	}
	if w = request(r, "POST", "/v1/products/2/details", productJSON(2, "HW5-001", 1)); w.Code != 204 {
		t.Fatal("old SKU should become reusable after update")
	}
}

type brokenStore struct{}

func (brokenStore) Get(int32) (Product, error) { return Product{}, errors.New("private store failure") }
func (brokenStore) Update(Product) error       { return errors.New("private store failure") }

func TestInternalErrorResponses(t *testing.T) {
	r := newRouter(brokenStore{}, serverOptions{APIKey: testKey})
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/v1/products/1", ""},
		{"POST", "/v1/products/1/details", productJSON(1, "NEW", 1)},
	} {
		w := request(r, tc.method, tc.path, tc.body)
		assertError(t, w, 500)
		if strings.Contains(w.Body.String(), "private") {
			t.Fatal("internal error details leaked")
		}
	}
}

func TestFaultInjectionRequiresExplicitOptIn(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		var logs bytes.Buffer
		r := newRouter(newMemoryStore(), serverOptions{APIKey: testKey, TestFaults: enabled,
			Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
		for _, method := range []string{"GET", "POST"} {
			path, body := "/v1/products/1", ""
			if method == "POST" {
				path, body = path+"/details", productJSON(1, "NEW", 1)
			}
			req := httptest.NewRequest(method, path, strings.NewReader(body))
			req.Header.Set("X-API-Key", testKey)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-HW5-Test-Failure", "panic")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if enabled {
				assertError(t, w, 500)
			} else if method == "GET" && w.Code != 200 || method == "POST" && w.Code != 204 {
				t.Fatalf("fault injection active without opt-in: %d", w.Code)
			}
		}
		lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
		if len(lines) != 2 {
			t.Fatalf("want one log per request, got %s", logs.String())
		}
		for _, line := range lines {
			var entry map[string]any
			if json.Unmarshal([]byte(line), &entry) != nil || entry["endpoint"] == nil || entry["duration_ms"] == nil || entry["status"] == nil {
				t.Fatalf("invalid request log: %s", line)
			}
			if strings.Contains(line, testKey) {
				t.Fatal("credentials leaked to logs")
			}
		}
	}
}

func signedToken(secret, header, payload string) string {
	encode := base64.RawURLEncoding.EncodeToString
	message := encode([]byte(header)) + "." + encode([]byte(payload))
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(message))
	return message + "." + encode(mac.Sum(nil))
}

func TestAuthentication(t *testing.T) {
	secret := "local-test-secret-at-least-32-bytes"
	r := newRouter(newMemoryStore(), serverOptions{APIKey: testKey, JWTSecret: secret})
	now := time.Now().Unix()
	valid := signedToken(secret, `{"alg":"HS256"}`, fmt.Sprintf(`{"exp":%d}`, now+3600))
	for _, tc := range []struct {
		name, key, bearer string
		status            int
	}{
		{"missing", "", "", 401},
		{"bad API key", "wrong", "", 401},
		{"API key", testKey, "", 200},
		{"Bearer", "", "Bearer " + valid, 200},
		{"either scheme suffices", testKey, "Bearer invalid", 200},
		{"wrong signature", "", "Bearer " + signedToken("wrong", `{"alg":"HS256"}`, fmt.Sprintf(`{"exp":%d}`, now+3600)), 401},
		{"expired", "", "Bearer " + signedToken(secret, `{"alg":"HS256"}`, fmt.Sprintf(`{"exp":%d}`, now-1)), 401},
		{"not yet valid", "", "Bearer " + signedToken(secret, `{"alg":"HS256"}`, fmt.Sprintf(`{"exp":%d,"nbf":%d}`, now+3600, now+600)), 401},
		{"missing expiry", "", "Bearer " + signedToken(secret, `{"alg":"HS256"}`, `{}`), 401},
		{"none algorithm", "", "Bearer " + signedToken(secret, `{"alg":"none"}`, fmt.Sprintf(`{"exp":%d}`, now+3600)), 401},
		{"malformed", "", "Bearer x.y.z", 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/v1/products/1", nil)
			req.Header.Set("X-API-Key", tc.key)
			req.Header.Set("Authorization", tc.bearer)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if tc.status != 200 {
				assertError(t, w, tc.status)
			} else if w.Code != 200 {
				t.Fatalf("status = %d", w.Code)
			}
		})
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/health", nil))
	if w.Code != 200 {
		t.Fatal("health must remain accessible without product credentials")
	}
}

func TestConcurrentReadsAndWrites(t *testing.T) {
	r := newRouter(newMemoryStore(), serverOptions{APIKey: testKey, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))})
	var wg sync.WaitGroup
	for worker := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 40 {
				w := request(r, "POST", "/v1/products/1/details", productJSON(1, "CONCURRENT", int32(worker*40+i)))
				if w.Code != 204 {
					t.Errorf("concurrent POST: %d", w.Code)
				}
				w = request(r, "GET", "/v1/products/1", "")
				var p Product
				if json.Unmarshal(w.Body.Bytes(), &p) != nil || w.Code != 200 || p.ProductID != 1 || p.SKU != "CONCURRENT" {
					t.Errorf("concurrent GET: %d %s", w.Code, w.Body.String())
				}
			}
		}()
	}
	wg.Wait()
}
