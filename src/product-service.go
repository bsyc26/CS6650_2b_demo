package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
)

// Product matches components.schemas.Product in ../api.yaml.
type Product struct {
	ProductID    int32  `json:"product_id"`
	SKU          string `json:"sku"`
	Manufacturer string `json:"manufacturer"`
	CategoryID   int32  `json:"category_id"`
	Weight       int32  `json:"weight"`
	SomeOtherID  int32  `json:"some_other_id"`
}

type APIError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
	Details string `json:"details,omitempty"`
}

// Pointers distinguish missing/null fields from valid zero weight.
type productInput struct {
	ProductID    *int32  `json:"product_id"`
	SKU          *string `json:"sku"`
	Manufacturer *string `json:"manufacturer"`
	CategoryID   *int32  `json:"category_id"`
	Weight       *int32  `json:"weight"`
	SomeOtherID  *int32  `json:"some_other_id"`
}

func (in productInput) validate(pathID int32) (Product, error) {
	if in.ProductID == nil || in.SKU == nil || in.Manufacturer == nil ||
		in.CategoryID == nil || in.Weight == nil || in.SomeOtherID == nil {
		return Product{}, errors.New("all six Product fields are required and must not be null")
	}
	if *in.ProductID < 1 || *in.CategoryID < 1 || *in.SomeOtherID < 1 || *in.Weight < 0 {
		return Product{}, errors.New("IDs must be positive int32 values and weight must be a nonnegative int32")
	}
	if *in.ProductID != pathID {
		return Product{}, errors.New("product_id must match the productId in the URL")
	}
	if n := utf8.RuneCountInString(*in.SKU); n < 1 || n > 100 {
		return Product{}, errors.New("sku must contain 1 to 100 characters")
	}
	if n := utf8.RuneCountInString(*in.Manufacturer); n < 1 || n > 200 {
		return Product{}, errors.New("manufacturer must contain 1 to 200 characters")
	}
	return Product{*in.ProductID, *in.SKU, *in.Manufacturer, *in.CategoryID, *in.Weight, *in.SomeOtherID}, nil
}

var (
	errNotFound     = errors.New("product not found")
	errDuplicateSKU = errors.New("sku is already used by another product")
)

// A store interface lets tests exercise genuine handler error paths.
type productStore interface {
	Get(int32) (Product, error)
	Update(Product) error
}

type memoryStore struct {
	mu       sync.RWMutex
	products map[int32]Product
	skus     map[string]int32
}

func newMemoryStore() *memoryStore {
	// The specification has no create endpoint. Seed known IDs so POST can
	// update existing products while preserving its documented 404 behavior.
	initial := []Product{
		{1, "HW5-001", "Example Maker", 1, 250, 1},
		{2, "HW5-002", "Example Maker", 1, 500, 2},
		{12345, "ABC-123-XYZ", "Acme Corporation", 456, 1250, 789},
	}
	s := &memoryStore{products: make(map[int32]Product), skus: make(map[string]int32)}
	for _, p := range initial {
		s.products[p.ProductID] = p
		s.skus[p.SKU] = p.ProductID
	}
	return s
}

func (s *memoryStore) Get(id int32) (Product, error) {
	s.mu.RLock()
	p, ok := s.products[id]
	s.mu.RUnlock()
	if !ok {
		return Product{}, errNotFound
	}
	return p, nil // A value copy; no shared mutable object escapes the lock.
}

func (s *memoryStore) Update(p Product) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.products[p.ProductID]
	if !ok {
		return errNotFound
	}
	if id, ok := s.skus[p.SKU]; ok && id != p.ProductID {
		return errDuplicateSKU
	}
	delete(s.skus, old.SKU)
	s.skus[p.SKU] = p.ProductID
	s.products[p.ProductID] = p
	return nil
}

type serverOptions struct {
	APIKey     string
	JWTSecret  string
	TestFaults bool
	Logger     *slog.Logger
	Metrics    *requestMetrics
}

func errorResponse(c *gin.Context, status int, code, message, details string) {
	c.AbortWithStatusJSON(status, APIError{code, message, details})
}

func newRouter(store productStore, opts serverOptions) *gin.Engine {
	r := gin.New()
	if opts.Metrics == nil {
		opts.Metrics = newRequestMetrics()
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	r.Use(func(c *gin.Context) {
		start := time.Now()
		c.Next()
		endpoint := c.FullPath()
		if endpoint == "" {
			endpoint = "unmatched"
		}
		duration := time.Since(start)
		opts.Metrics.observe(c.Request.Method, endpoint, c.Writer.Status(), duration.Seconds())
		opts.Logger.Info("request", "method", c.Request.Method, "endpoint", endpoint,
			"status", c.Writer.Status(), "duration_ms", float64(duration.Microseconds())/1000)
	})
	r.Use(func(c *gin.Context) {
		defer func() {
			if recover() != nil {
				errorResponse(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", "")
			}
		}()
		c.Next()
	})
	r.GET("/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	r.GET("/metrics", gin.WrapH(opts.Metrics.handler()))
	for _, prefix := range []string{"/v1", ""} {
		group := r.Group(prefix)
		group.Use(func(c *gin.Context) {
			if opts.APIKey != "" && hmac.Equal([]byte(c.GetHeader("X-API-Key")), []byte(opts.APIKey)) {
				c.Next()
				return
			}
			if validBearer(c.GetHeader("Authorization"), opts.JWTSecret, time.Now()) {
				c.Next()
				return
			}
			c.Header("WWW-Authenticate", `Bearer realm="product-api"`)
			errorResponse(c, http.StatusUnauthorized, "UNAUTHORIZED", "A valid API key or Bearer token is required", "")
		})
		group.Use(func(c *gin.Context) {
			// Explicit opt-in for local response-code demonstrations. The normal
			// server ignores this header. No special product IDs or random errors.
			if opts.TestFaults && c.GetHeader("X-HW5-Test-Failure") == "panic" {
				panic("requested local test failure")
			}
			c.Next()
		})
		group.GET("/products/:productId", func(c *gin.Context) {
			id, err := parseProductID(c.Param("productId"))
			if err != nil {
				// GET lists no 400 response: malformed IDs cannot identify a resource.
				errorResponse(c, http.StatusNotFound, "PRODUCT_NOT_FOUND", "Product not found", "")
				return
			}
			p, err := store.Get(id)
			if errors.Is(err, errNotFound) {
				errorResponse(c, http.StatusNotFound, "PRODUCT_NOT_FOUND", "Product not found", "")
			} else if err != nil {
				errorResponse(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", "")
			} else {
				c.JSON(http.StatusOK, p)
			}
		})
		group.POST("/products/:productId/details", func(c *gin.Context) {
			id, err := parseProductID(c.Param("productId"))
			if err != nil {
				errorResponse(c, http.StatusBadRequest, "INVALID_INPUT", "Invalid input data", err.Error())
				return
			}
			if c.ContentType() != "application/json" {
				errorResponse(c, http.StatusBadRequest, "INVALID_INPUT", "Invalid input data", "Content-Type must be application/json")
				return
			}
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
			dec := json.NewDecoder(c.Request.Body)
			var input productInput
			if err := dec.Decode(&input); err != nil {
				errorResponse(c, http.StatusBadRequest, "INVALID_INPUT", "Invalid input data", "Body must be one JSON Product object with valid field types")
				return
			}
			if err := dec.Decode(new(any)); err != io.EOF {
				errorResponse(c, http.StatusBadRequest, "INVALID_INPUT", "Invalid input data", "Body must contain exactly one JSON object")
				return
			}
			p, err := input.validate(id)
			if err != nil {
				errorResponse(c, http.StatusBadRequest, "INVALID_INPUT", "Invalid input data", err.Error())
				return
			}
			switch err := store.Update(p); {
			case errors.Is(err, errNotFound):
				errorResponse(c, http.StatusNotFound, "PRODUCT_NOT_FOUND", "Product not found", "")
			case errors.Is(err, errDuplicateSKU):
				errorResponse(c, http.StatusBadRequest, "INVALID_INPUT", "Invalid input data", err.Error())
			case err != nil:
				errorResponse(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", "")
			default:
				c.Status(http.StatusNoContent)
			}
		})
	}
	r.NoRoute(func(c *gin.Context) {
		errorResponse(c, http.StatusNotFound, "NOT_FOUND", "Route not found", "")
	})
	return r
}

func parseProductID(raw string) (int32, error) {
	id, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || id < 1 {
		return 0, errors.New("productId must be an integer between 1 and 2147483647")
	}
	return int32(id), nil
}

// The spec does not identify a token issuer. For the local exercise, the
// optional Bearer scheme accepts HS256 JWTs from the configured shared secret.
// This is not an implementation of an external identity provider.
func validBearer(header, secret string, now time.Time) bool {
	fields := strings.Fields(header)
	if secret == "" || len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") || len(fields[1]) > 8192 {
		return false
	}
	parts := strings.Split(fields[1], ".")
	if len(parts) != 3 {
		return false
	}
	decode := base64.RawURLEncoding.DecodeString
	meta, err := decode(parts[0])
	if err != nil {
		return false
	}
	var algorithm struct {
		Alg  string   `json:"alg"`
		Crit []string `json:"crit"`
	}
	if json.Unmarshal(meta, &algorithm) != nil || algorithm.Alg != "HS256" || len(algorithm.Crit) != 0 {
		return false
	}
	signature, err := decode(parts[2])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return false
	}
	payload, err := decode(parts[1])
	if err != nil {
		return false
	}
	var claims struct {
		Expiry    *int64 `json:"exp"`
		NotBefore *int64 `json:"nbf"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.Expiry == nil || now.Unix() >= *claims.Expiry {
		return false
	}
	return claims.NotBefore == nil || now.Unix() >= *claims.NotBefore
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	apiKey, jwtSecret := os.Getenv("PRODUCT_API_KEY"), os.Getenv("PRODUCT_JWT_SECRET")
	if apiKey == "" && jwtSecret == "" {
		logger.Error("set PRODUCT_API_KEY or PRODUCT_JWT_SECRET before starting the server")
		os.Exit(1)
	}
	if jwtSecret != "" && len(jwtSecret) < 32 {
		logger.Error("PRODUCT_JWT_SECRET must contain at least 32 bytes")
		os.Exit(1)
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	gin.SetMode(gin.ReleaseMode)
	server := &http.Server{
		Addr: ":" + port,
		Handler: newRouter(newMemoryStore(), serverOptions{
			APIKey: apiKey, JWTSecret: jwtSecret,
			TestFaults: os.Getenv("PRODUCT_TEST_FAULTS") == "true", Logger: logger,
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	logger.Info("listening", "address", server.Addr, "test_faults", os.Getenv("PRODUCT_TEST_FAULTS") == "true")
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error(fmt.Sprint(err))
		os.Exit(1)
	}
}
