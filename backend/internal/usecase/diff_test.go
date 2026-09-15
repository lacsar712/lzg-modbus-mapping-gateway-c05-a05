package usecase

import (
	"testing"

	"github.com/bytecode/modbus-mapping-gateway/internal/domain"
	"github.com/bytecode/modbus-mapping-gateway/internal/port"
)

const baseYAML = `
devices:
  - id: plc-line-a
    name: 产线A模拟PLC
    endpoint: mock-plc:5020
    unitId: 1
    timeoutMs: 2000
    points:
      - name: motor_rpm
        address: 0
        type: float32_abcd
        writable: true
        scale: 1
        offset: 0
        min: 0
        max: 5000
      - name: pressure
        address: 20
        type: int16
        writable: true
        scale: 0.1
        offset: 0
        min: 0
        max: 50
`

// fakeStore records Save calls so tests can prove invalid candidates never
// reach the store ("不落地").
type fakeStore struct {
	text    string
	saves   int
	saveErr error
}

func (f *fakeStore) Load() (domain.MappingConfig, string, error) {
	return parseYAML(f.text)
}
func (f *fakeStore) Save(t string) error {
	f.saves++
	if f.saveErr != nil {
		return f.saveErr
	}
	f.text = t
	return nil
}
func (f *fakeStore) Path() string { return "fake" }

type stubModbus struct{}

func (stubModbus) ReadHoldingRegisters(string, byte, int, uint16, uint16) ([]uint16, error) {
	return nil, nil
}
func (stubModbus) WriteSingleRegister(string, byte, int, uint16, uint16) error {
	return nil
}
func (stubModbus) WriteMultipleRegisters(string, byte, int, uint16, []uint16) error {
	return nil
}

var _ port.ModbusClient = stubModbus{}

func newTestService(t *testing.T) (*GatewayService, *fakeStore) {
	t.Helper()
	st := &fakeStore{text: baseYAML}
	svc, err := NewGatewayService(st, stubModbus{})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return svc, st
}

// Changing one point's address must surface as a single point-change entry
// with an address old->new field change.
func TestPreviewAddressChangeDiff(t *testing.T) {
	svc, st := newTestService(t)

	candidate := `
devices:
  - id: plc-line-a
    name: 产线A模拟PLC
    endpoint: mock-plc:5020
    unitId: 1
    timeoutMs: 2000
    points:
      - name: motor_rpm
        address: 5
        type: float32_abcd
        writable: true
        scale: 1
        offset: 0
        min: 0
        max: 5000
      - name: pressure
        address: 20
        type: int16
        writable: true
        scale: 0.1
        offset: 0
        min: 0
        max: 50
`
	diff, err := svc.Preview(candidate)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if diff.Identical {
		t.Fatal("expected non-identical diff")
	}
	if st.saves != 0 {
		t.Fatalf("preview must not save, got %d saves", st.saves)
	}
	if diff.Summary.PointsChanged != 1 {
		t.Fatalf("want 1 point changed, got %d (entries=%v)", diff.Summary.PointsChanged, diff.Entries)
	}
	var found bool
	for _, e := range diff.Entries {
		if e.Op == DiffPointChg && e.Point == "motor_rpm" {
			found = true
			if len(e.Changes) != 1 || e.Changes[0].Field != "address" {
				t.Fatalf("want single address change, got %+v", e.Changes)
			}
			if e.Changes[0].Old.(uint16) != 0 || e.Changes[0].New.(uint16) != 5 {
				t.Fatalf("address 0->5 expected, got %v -> %v", e.Changes[0].Old, e.Changes[0].New)
			}
		}
	}
	if !found {
		t.Fatalf("motor_rpm point-change entry missing: %+v", diff.Entries)
	}

	// Preview is read-only: active config must still see address 0.
	_, p, err := svc.GetPoint("plc-line-a", "motor_rpm")
	if err != nil {
		t.Fatal(err)
	}
	if p.Address != 0 {
		t.Fatalf("active config mutated by preview: address=%d", p.Address)
	}
}

// An invalid candidate (unsupported type) must be rejected by Preview,
// DryRun and ReloadFromText; the store must never be written and the active
// config must stay on the old mapping.
func TestInvalidCandidateNeverLands(t *testing.T) {
	svc, st := newTestService(t)

	invalid := `
devices:
  - id: plc-line-a
    name: 产线A模拟PLC
    endpoint: mock-plc:5020
    unitId: 1
    points:
      - name: motor_rpm
        address: 0
        type: float64_bogus
        writable: true
        scale: 1
`
	if _, err := svc.Preview(invalid); err == nil {
		t.Fatal("preview should reject unsupported type")
	}
	if st.saves != 0 {
		t.Fatalf("preview saved despite invalid candidate: %d", st.saves)
	}

	rep, err := svc.DryRun(invalid)
	if err == nil {
		t.Fatal("dry-run should reject unsupported type")
	}
	if rep.Valid {
		t.Fatal("dry-run report must be invalid")
	}
	if st.saves != 0 {
		t.Fatalf("dry-run saved despite invalid candidate: %d", st.saves)
	}

	if err := svc.ReloadFromText(invalid); err == nil {
		t.Fatal("apply should reject unsupported type")
	}
	if st.saves != 0 {
		t.Fatalf("invalid candidate must not reach store.Save, got %d saves", st.saves)
	}

	// Old config still active and served.
	_, p, err := svc.GetPoint("plc-line-a", "pressure")
	if err != nil {
		t.Fatalf("old config lost after invalid apply: %v", err)
	}
	if p.Address != 20 || p.Type != domain.TypeInt16 {
		t.Fatalf("pressure point changed unexpectedly: %+v", p)
	}
	if len(svc.ListDevices()) != 1 {
		t.Fatal("device set changed after invalid apply")
	}
}

// Malformed YAML (parse error, not just validation) must also leave the
// active config untouched.
func TestMalformedYAMLNeverLands(t *testing.T) {
	svc, st := newTestService(t)

	if err := svc.ReloadFromText("devices: [oops\n  yaml: :"); err == nil {
		t.Fatal("expected parse error")
	}
	if st.saves != 0 {
		t.Fatalf("malformed yaml reached store: %d saves", st.saves)
	}
	if _, _, err := svc.GetPoint("plc-line-a", "motor_rpm"); err != nil {
		t.Fatalf("old config lost after malformed candidate: %v", err)
	}
}

// A valid candidate applies and becomes the active config; dry-run reports the
// merged read windows.
func TestValidCandidateAppliesAndDryRun(t *testing.T) {
	svc, st := newTestService(t)

	candidate := `
devices:
  - id: plc-line-a
    name: 产线A模拟PLC
    endpoint: mock-plc:5020
    unitId: 1
    points:
      - name: motor_rpm
        address: 0
        type: float32_abcd
        writable: true
        scale: 1
`
	rep, err := svc.DryRun(candidate)
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if !rep.Valid || rep.DeviceCount != 1 || rep.PointCount != 1 {
		t.Fatalf("unexpected report: %+v", rep)
	}
	if len(rep.Windows) != 1 || rep.Windows[0].Start != 0 || rep.Windows[0].Count != 2 {
		t.Fatalf("want one window 0/2, got %+v", rep.Windows)
	}

	if err := svc.ReloadFromText(candidate); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if st.saves != 1 {
		t.Fatalf("valid apply must save exactly once, got %d", st.saves)
	}
	if pts, err := svc.GetPoints("plc-line-a"); err != nil || len(pts) != 1 {
		t.Fatalf("candidate not active: pts=%v err=%v", pts, err)
	}
}

// Point add/remove detection for the structured diff.
func TestPreviewPointAddRemove(t *testing.T) {
	svc, _ := newTestService(t)

	candidate := `
devices:
  - id: plc-line-a
    name: 产线A模拟PLC
    endpoint: mock-plc:5020
    unitId: 1
    points:
      - name: motor_rpm
        address: 0
        type: float32_abcd
        writable: true
        scale: 1
      - name: status_word
        address: 30
        type: uint16
        writable: false
        scale: 1
`
	diff, err := svc.Preview(candidate)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if diff.Summary.PointsAdded != 1 || diff.Summary.PointsRemoved != 1 {
		t.Fatalf("want +1/-1 points, got %+v", diff.Summary)
	}
	adds, removes := map[string]bool{}, map[string]bool{}
	for _, e := range diff.Entries {
		if e.Op == DiffPointAdd {
			adds[e.Point] = true
		}
		if e.Op == DiffPointDel {
			removes[e.Point] = true
		}
	}
	if !adds["status_word"] || !removes["pressure"] {
		t.Fatalf("want add status_word / remove pressure, got +%v -%v", adds, removes)
	}
}
