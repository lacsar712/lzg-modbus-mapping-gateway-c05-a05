package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bytecode/modbus-mapping-gateway/internal/domain"
	"github.com/bytecode/modbus-mapping-gateway/internal/usecase"
	"gopkg.in/yaml.v3"
)

type memStore struct{ text string }

func (m *memStore) Load() (domain.MappingConfig, string, error) {
	var cfg domain.MappingConfig
	if err := yaml.Unmarshal([]byte(m.text), &cfg); err != nil {
		return domain.MappingConfig{}, "", err
	}
	return cfg, m.text, nil
}
func (m *memStore) Save(t string) error { m.text = t; return nil }
func (m *memStore) Path() string        { return "mem" }

type noopModbus struct{}

func (noopModbus) ReadHoldingRegisters(string, byte, int, uint16, uint16) ([]uint16, error) {
	return nil, nil
}
func (noopModbus) WriteSingleRegister(string, byte, int, uint16, uint16) error { return nil }
func (noopModbus) WriteMultipleRegisters(string, byte, int, uint16, []uint16) error {
	return nil
}

const validMapping = `
devices:
  - id: plc-1
    name: PLC1
    endpoint: plc:5020
    unitId: 1
    points:
      - name: rpm
        address: 0
        type: float32_abcd
        writable: true
        scale: 1
`

func newTestRouter(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	svc, err := usecase.NewGatewayService(&memStore{text: validMapping}, noopModbus{})
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	srv := NewServer(svc)
	return srv, srv.Router()
}

func loginToken(t *testing.T, r http.Handler, user, pass string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": user, "password": pass})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var resp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || resp.Token == "" {
		t.Fatalf("login failed: status=%d body=%s", w.Code, w.Body.String())
	}
	return resp.Token
}

func postYAML(t *testing.T, r http.Handler, token, path, yaml string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"yaml": yaml})
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

const invalidTypeMapping = `
devices:
  - id: plc-1
    name: PLC1
    endpoint: plc:5020
    unitId: 1
    points:
      - name: rpm
        address: 0
        type: float64_bogus
        writable: true
        scale: 1
`

// Invalid candidate through the real HTTP route: 400 + keptOld, and a
// follow-up GET /mapping still returns the old YAML.
func TestPreviewInvalidCandidateKeepsOld(t *testing.T) {
	srv, r := newTestRouter(t)
	token := loginToken(t, r, "engineer", "mod123456")

	w := postYAML(t, r, token, "/api/mapping/preview", invalidTypeMapping)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["keptOld"] != true {
		t.Fatalf("want keptOld=true, got %v", resp["keptOld"])
	}
	if resp["error"] == nil || resp["error"] == "" {
		t.Fatal("want error message")
	}
	if cur := srv.svc.YAMLText(); cur != validMapping {
		t.Fatalf("active YAML changed after rejected preview:\n%s", cur)
	}

	// Same guarantee on dry-run.
	w = postYAML(t, r, token, "/api/mapping/dry-run", invalidTypeMapping)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("dry-run want 400, got %d", w.Code)
	}
}

// A valid address change produces a structured diff over HTTP.
func TestPreviewValidDiffOverHTTP(t *testing.T) {
	_, r := newTestRouter(t)
	token := loginToken(t, r, "engineer", "mod123456")

	w := postYAML(t, r, token, "/api/mapping/preview", `
devices:
  - id: plc-1
    name: PLC1
    endpoint: plc:5020
    unitId: 1
    points:
      - name: rpm
        address: 8
        type: float32_abcd
        writable: true
        scale: 1
`)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Diff struct {
			Identical bool `json:"identical"`
			Summary   struct {
				PointsChanged int `json:"pointsChanged"`
			} `json:"summary"`
		} `json:"diff"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Diff.Identical || resp.Diff.Summary.PointsChanged != 1 {
		t.Fatalf("unexpected diff: %s", w.Body.String())
	}
}

// Observer role may not preview candidates.
func TestPreviewRequiresEngineer(t *testing.T) {
	_, r := newTestRouter(t)
	token := loginToken(t, r, "observer", "obs123456")
	w := postYAML(t, r, token, "/api/mapping/preview", validMapping)
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", w.Code)
	}
}
