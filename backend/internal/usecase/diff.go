package usecase

import (
	"fmt"

	"github.com/bytecode/modbus-mapping-gateway/internal/domain"
)

// DiffOp is the change kind of one diff entry.
type DiffOp string

const (
	DiffAdd      DiffOp = "add"    // entry is new
	DiffRemove   DiffOp = "remove" // entry existed only in the current config
	DiffChange   DiffOp = "change" // same identity, fields differ
	DiffPointAdd DiffOp = "point-add"
	DiffPointDel DiffOp = "point-remove"
	DiffPointChg DiffOp = "point-change"
)

// FieldChange is one field-level old -> new value pair.
type FieldChange struct {
	Field string      `json:"field"`
	Old   interface{} `json:"old,omitempty"`
	New   interface{} `json:"new,omitempty"`
}

// DiffEntry describes one structural difference between current and candidate.
type DiffEntry struct {
	Op       DiffOp        `json:"op"`
	DeviceID string        `json:"deviceId"`
	Device   string        `json:"device,omitempty"`  // device name, for context
	Point    string        `json:"point,omitempty"`   // point name, for context
	Address  *uint16       `json:"address,omitempty"` // point address, for context
	Changes  []FieldChange `json:"changes,omitempty"`
}

// ConfigDiff is the structured diff between the active config and a candidate.
type ConfigDiff struct {
	Summary   DiffSummary `json:"summary"`
	Entries   []DiffEntry `json:"entries"`
	Identical bool        `json:"identical"`
}

// DiffSummary holds change counters.
type DiffSummary struct {
	DevicesAdded   int `json:"devicesAdded"`
	DevicesRemoved int `json:"devicesRemoved"`
	DevicesChanged int `json:"devicesChanged"`
	PointsAdded    int `json:"pointsAdded"`
	PointsRemoved  int `json:"pointsRemoved"`
	PointsChanged  int `json:"pointsChanged"`
}

// pointFields is the ordered list of point fields compared by the diff.
var pointFields = []string{"address", "type", "bit", "writable", "scale", "offset", "min", "max"}

func pointField(p domain.PointDef, field string) interface{} {
	switch field {
	case "address":
		return p.Address
	case "type":
		return string(p.Type)
	case "bit":
		if p.Bit == nil {
			return nil
		}
		return *p.Bit
	case "writable":
		return p.Writable
	case "scale":
		return p.Scale
	case "offset":
		return p.Offset
	case "min":
		if p.Min == nil {
			return nil
		}
		return *p.Min
	case "max":
		if p.Max == nil {
			return nil
		}
		return *p.Max
	}
	return nil
}

func diffPoints(device string, oldPts, newPts []domain.PointDef) ([]DiffEntry, *DiffSummary) {
	var entries []DiffEntry
	var sum DiffSummary
	oldByName := map[string]domain.PointDef{}
	newByName := map[string]domain.PointDef{}
	for _, p := range oldPts {
		oldByName[p.Name] = p
	}
	for _, p := range newPts {
		newByName[p.Name] = p
	}
	// deterministic order: candidate order first, then removed points in old order
	seen := map[string]struct{}{}
	for _, np := range newPts {
		seen[np.Name] = struct{}{}
		op, ok := oldByName[np.Name]
		if !ok {
			addr := np.Address
			entries = append(entries, DiffEntry{
				Op: DiffPointAdd, DeviceID: device, Device: device, Point: np.Name, Address: &addr,
			})
			sum.PointsAdded++
			continue
		}
		var changes []FieldChange
		for _, f := range pointFields {
			ov, nv := pointField(op, f), pointField(np, f)
			if !valuesEqual(ov, nv) {
				changes = append(changes, FieldChange{Field: f, Old: ov, New: nv})
			}
		}
		if len(changes) > 0 {
			addr := np.Address
			entries = append(entries, DiffEntry{
				Op: DiffPointChg, DeviceID: device, Device: device, Point: np.Name, Address: &addr, Changes: changes,
			})
			sum.PointsChanged++
		}
	}
	for _, op := range oldPts {
		if _, ok := seen[op.Name]; ok {
			continue
		}
		addr := op.Address
		entries = append(entries, DiffEntry{
			Op: DiffPointDel, DeviceID: device, Device: device, Point: op.Name, Address: &addr,
		})
		sum.PointsRemoved++
	}
	return entries, &sum
}

func valuesEqual(a, b interface{}) bool {
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

// DiffConfig builds a structured diff between two validated configs.
// Identity is device id and point name.
func DiffConfig(oldCfg, newCfg domain.MappingConfig) ConfigDiff {
	diff := ConfigDiff{Entries: []DiffEntry{}}
	oldByID := map[string]domain.DeviceDef{}
	newByID := map[string]domain.DeviceDef{}
	for _, d := range oldCfg.Devices {
		oldByID[d.ID] = d
	}
	for _, d := range newCfg.Devices {
		newByID[d.ID] = d
	}

	seen := map[string]struct{}{}
	for _, nd := range newCfg.Devices {
		seen[nd.ID] = struct{}{}
		od, ok := oldByID[nd.ID]
		if !ok {
			entry := DiffEntry{Op: DiffAdd, DeviceID: nd.ID, Device: nd.Name}
			entry.Changes = []FieldChange{
				{Field: "name", New: nd.Name},
				{Field: "endpoint", New: nd.Endpoint},
				{Field: "unitId", New: nd.UnitID},
				{Field: "timeoutMs", New: nd.TimeoutMs},
			}
			diff.Entries = append(diff.Entries, entry)
			diff.Summary.DevicesAdded++
			// all points of a new device are additions
			for _, p := range nd.Points {
				addr := p.Address
				diff.Entries = append(diff.Entries, DiffEntry{
					Op: DiffPointAdd, DeviceID: nd.ID, Device: nd.Name, Point: p.Name, Address: &addr,
				})
				diff.Summary.PointsAdded++
			}
			continue
		}
		var devChanges []FieldChange
		if od.Name != nd.Name {
			devChanges = append(devChanges, FieldChange{Field: "name", Old: od.Name, New: nd.Name})
		}
		if od.Endpoint != nd.Endpoint {
			devChanges = append(devChanges, FieldChange{Field: "endpoint", Old: od.Endpoint, New: nd.Endpoint})
		}
		if od.UnitID != nd.UnitID {
			devChanges = append(devChanges, FieldChange{Field: "unitId", Old: od.UnitID, New: nd.UnitID})
		}
		if od.TimeoutMs != nd.TimeoutMs {
			devChanges = append(devChanges, FieldChange{Field: "timeoutMs", Old: od.TimeoutMs, New: nd.TimeoutMs})
		}
		if len(devChanges) > 0 {
			diff.Entries = append(diff.Entries, DiffEntry{
				Op: DiffChange, DeviceID: nd.ID, Device: nd.Name, Changes: devChanges,
			})
			diff.Summary.DevicesChanged++
		}
		pEntries, pSum := diffPoints(nd.ID, od.Points, nd.Points)
		diff.Entries = append(diff.Entries, pEntries...)
		diff.Summary.PointsAdded += pSum.PointsAdded
		diff.Summary.PointsRemoved += pSum.PointsRemoved
		diff.Summary.PointsChanged += pSum.PointsChanged
	}

	for _, od := range oldCfg.Devices {
		if _, ok := seen[od.ID]; ok {
			continue
		}
		entry := DiffEntry{Op: DiffRemove, DeviceID: od.ID, Device: od.Name}
		entry.Changes = []FieldChange{
			{Field: "name", Old: od.Name},
			{Field: "endpoint", Old: od.Endpoint},
			{Field: "unitId", Old: od.UnitID},
			{Field: "timeoutMs", Old: od.TimeoutMs},
		}
		diff.Entries = append(diff.Entries, entry)
		diff.Summary.DevicesRemoved++
		for _, p := range od.Points {
			addr := p.Address
			diff.Entries = append(diff.Entries, DiffEntry{
				Op: DiffPointDel, DeviceID: od.ID, Device: od.Name, Point: p.Name, Address: &addr,
			})
			diff.Summary.PointsRemoved++
		}
	}

	diff.Identical = len(diff.Entries) == 0
	return diff
}

// ReadWindow is one merged modbus read window after a candidate apply.
type ReadWindow struct {
	DeviceID string `json:"deviceId"`
	Start    uint16 `json:"start"`
	Count    uint16 `json:"count"`
}

// DryRunReport is the validation-only result for a candidate YAML.
// It never mutates the active configuration.
type DryRunReport struct {
	Valid       bool         `json:"valid"`
	Error       string       `json:"error,omitempty"`
	DeviceCount int          `json:"deviceCount"`
	PointCount  int          `json:"pointCount"`
	Windows     []ReadWindow `json:"windows,omitempty"`
}

// Preview parses + validates a candidate and returns its structured diff against
// the active config. Nothing is saved or applied.
func (s *GatewayService) Preview(text string) (ConfigDiff, error) {
	candidate, err := s.parseCandidate(text)
	if err != nil {
		return ConfigDiff{Entries: []DiffEntry{}, Identical: true}, err
	}
	return DiffConfig(s.Config(), candidate), nil
}

// DryRun parses + validates a candidate and summarises the merged modbus read
// windows that would be used after apply. Nothing is saved or applied.
func (s *GatewayService) DryRun(text string) (DryRunReport, error) {
	candidate, err := s.parseCandidate(text)
	if err != nil {
		return DryRunReport{Valid: false}, err
	}
	rep := DryRunReport{Valid: true, Windows: []ReadWindow{}}
	for _, d := range candidate.Devices {
		rep.DeviceCount++
		rep.PointCount += len(d.Points)
		for _, r := range domain.MergeRanges(domain.BuildPointRanges(d.Points), 4) {
			rep.Windows = append(rep.Windows, ReadWindow{
				DeviceID: d.ID, Start: r.Start, Count: r.Count,
			})
		}
	}
	return rep, nil
}

// parseCandidate parses, validates and normalizes a candidate YAML exactly the
// way Reload does, without touching the active config or the store.
func (s *GatewayService) parseCandidate(text string) (domain.MappingConfig, error) {
	cfg, _, err := parseYAML(text)
	if err != nil {
		return domain.MappingConfig{}, err
	}
	if err := cfg.Validate(); err != nil {
		return domain.MappingConfig{}, fmt.Errorf("invalid mapping: %w", err)
	}
	normalizeConfig(&cfg)
	return cfg, nil
}

// normalizeConfig applies the same defaults as Reload: timeout and point
// scale normalization.
func normalizeConfig(cfg *domain.MappingConfig) {
	for i := range cfg.Devices {
		if cfg.Devices[i].TimeoutMs <= 0 {
			cfg.Devices[i].TimeoutMs = 2000
		}
		for j := range cfg.Devices[i].Points {
			cfg.Devices[i].Points[j] = cfg.Devices[i].Points[j].Normalize()
		}
	}
}
