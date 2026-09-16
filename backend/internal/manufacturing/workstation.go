package manufacturing

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Workstation status.
type WorkstationStatus int16

const (
	WorkstationInactive WorkstationStatus = 0
	WorkstationActive   WorkstationStatus = 1
)

// Workstation is a production resource with a daily capacity in minutes
// (Dolibarr core has no workstation capacity model; this is new structure).
type Workstation struct {
	ID               int64             `json:"id"`
	EntityID         int64             `json:"entity_id"`
	Code             string            `json:"code"` // unique per entity
	Label            string            `json:"label"`
	DailyCapacityMin int64             `json:"daily_capacity_min"`
	Status           WorkstationStatus `json:"status"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
	RowVersion       int64             `json:"row_version"`
}

// Validate checks workstation invariants.
func (w Workstation) Validate() error {
	if w.EntityID <= 0 {
		return errors.New("manufacturing: entity_id required")
	}
	if strings.TrimSpace(w.Code) == "" {
		return errors.New("manufacturing: code required")
	}
	if strings.TrimSpace(w.Label) == "" {
		return errors.New("manufacturing: label required")
	}
	if w.DailyCapacityMin <= 0 {
		return errors.New("manufacturing: daily_capacity_min must be positive")
	}
	return nil
}

// BOMOperation is one routing step on a BOM: the workstation that runs it,
// the per-unit run time and the per-run setup time (all int64 minutes).
type BOMOperation struct {
	ID                int64 `json:"id"`
	EntityID          int64 `json:"entity_id"`
	BOMID             int64 `json:"bom_id"`
	Seq               int32 `json:"seq"` // 1-based order within the routing
	WorkstationID     int64 `json:"workstation_id"`
	RunMinutesPerUnit int64 `json:"run_minutes_per_unit"`
	SetupMinutes      int64 `json:"setup_minutes"`
}

// Validate checks routing-step invariants.
func (o BOMOperation) Validate() error {
	if o.EntityID <= 0 || o.BOMID <= 0 {
		return errors.New("manufacturing: entity_id and bom_id required")
	}
	if o.Seq <= 0 {
		return errors.New("manufacturing: seq must be positive")
	}
	if o.WorkstationID <= 0 {
		return errors.New("manufacturing: workstation_id required")
	}
	if o.RunMinutesPerUnit < 0 || o.SetupMinutes < 0 {
		return errors.New("manufacturing: operation minutes cannot be negative")
	}
	return nil
}

// PlannedMinutes scales one routing step to an order quantity.
func (o BOMOperation) PlannedMinutes(qty int64) int64 {
	return o.SetupMinutes + o.RunMinutesPerUnit*qty
}

// MO operation status.
type MOOperationStatus int16

const (
	MOOpPending  MOOperationStatus = 0
	MOOpDone     MOOperationStatus = 1
	MOOpCanceled MOOperationStatus = -1
)

// MOOperation is one scheduled routing step on a manufacturing order.
type MOOperation struct {
	ID             int64             `json:"id"`
	EntityID       int64             `json:"entity_id"`
	MOID           int64             `json:"mo_id"`
	Seq            int32             `json:"seq"`
	WorkstationID  int64             `json:"workstation_id"`
	PlannedMinutes int64             `json:"planned_minutes"`
	ScheduledStart time.Time         `json:"scheduled_start"`
	ScheduledEnd   time.Time         `json:"scheduled_end"`
	Status         MOOperationStatus `json:"status"`
	Overloaded     bool              `json:"overloaded"`
	ActualMinutes  int64             `json:"actual_minutes"`
	RowVersion     int64             `json:"row_version"`
}

// dayKey formats a day for load maps.
func dayKey(t time.Time) string { return t.UTC().Format("2006-01-02") }

// midnightUTC truncates to the UTC calendar day.
func midnightUTC(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// DayLoad maps workstationID -> day ("YYYY-MM-DD") -> already-booked minutes.
// It carries both pre-existing bookings (other MOs) and the running totals of
// the schedule being built, so overload is visible instead of resolved.
type DayLoad map[int64]map[string]int64

func copyLoad(in DayLoad) DayLoad {
	out := DayLoad{}
	for ws, days := range in {
		m := map[string]int64{}
		for d, v := range days {
			m[d] = v
		}
		out[ws] = m
	}
	return out
}

// ScheduleOperations forward-schedules routing x qty from start.
//
// Model (documented lite scope):
//   - Operations chain sequentially: each step starts on the day the previous
//     step ends (routing dependency; parallel workstations are not overlapped).
//   - Whole-day buckets: a step needing more than one day of its workstation
//     spreads evenly across consecutive days (first days take the remainder).
//   - Overload is FLAGGED, never force-resolved: when a step's day load
//     (prior bookings + this schedule) exceeds the workstation's daily
//     capacity, the operation is still scheduled and marked Overloaded.
//   - Days are UTC midnights; the workday is assumed to start at 00:00 UTC.
//   - Capacities carry workstation daily capacities by id; prior carries
//     pre-existing bookings. Missing/zero capacity is a validation error.
func ScheduleOperations(routing []BOMOperation, qty int64, start time.Time, capacities map[int64]int64, prior DayLoad) ([]MOOperation, error) {
	if qty <= 0 {
		return nil, fmt.Errorf("manufacturing: production qty must be positive: %w", platform.ErrValidation)
	}
	if len(routing) == 0 {
		return nil, fmt.Errorf("manufacturing: routing is empty: %w", platform.ErrValidation)
	}
	steps := append([]BOMOperation(nil), routing...)
	sort.Slice(steps, func(i, j int) bool { return steps[i].Seq < steps[j].Seq })
	for i := 1; i < len(steps); i++ {
		if steps[i].Seq == steps[i-1].Seq {
			return nil, fmt.Errorf("manufacturing: duplicate routing seq %d: %w", steps[i].Seq, platform.ErrValidation)
		}
	}
	loads := copyLoad(prior)
	cursor := midnightUTC(start)
	out := make([]MOOperation, 0, len(steps))
	for _, s := range steps {
		if err := s.Validate(); err != nil {
			return nil, fmt.Errorf("%w: %w", err, platform.ErrValidation)
		}
		capMin, ok := capacities[s.WorkstationID]
		if !ok || capMin <= 0 {
			return nil, fmt.Errorf("manufacturing: workstation %d has no capacity: %w", s.WorkstationID, platform.ErrValidation)
		}
		total := s.PlannedMinutes(qty)
		if total <= 0 {
			return nil, fmt.Errorf("manufacturing: operation seq %d plans zero minutes: %w", s.Seq, platform.ErrValidation)
		}
		days := int((total + capMin - 1) / capMin) // ceil
		base := total / int64(days)
		rem := total % int64(days)
		overloaded := false
		for k := 0; k < days; k++ {
			slice := base
			if int64(k) < rem {
				slice++
			}
			day := cursor.AddDate(0, 0, k)
			key := dayKey(day)
			m := loads[s.WorkstationID]
			if m == nil {
				m = map[string]int64{}
				loads[s.WorkstationID] = m
			}
			m[key] += slice
			if m[key] > capMin {
				overloaded = true
			}
		}
		out = append(out, MOOperation{
			EntityID:       s.EntityID,
			Seq:            s.Seq,
			WorkstationID:  s.WorkstationID,
			PlannedMinutes: total,
			ScheduledStart: cursor,
			ScheduledEnd:   cursor.AddDate(0, 0, days),
			Status:         MOOpPending,
			Overloaded:     overloaded,
		})
		cursor = cursor.AddDate(0, 0, days)
	}
	return out, nil
}

// CapacityDay is one workstation-day of load vs capacity.
type CapacityDay struct {
	Date            string `json:"date"`
	WorkstationID   int64  `json:"workstation_id"`
	LoadMinutes     int64  `json:"load_minutes"`
	CapacityMinutes int64  `json:"capacity_minutes"`
	Overloaded      bool   `json:"overloaded"`
}

// spreadOp attributes an operation's planned minutes across the UTC days it
// occupies, with the same even-split rule as the scheduler (remainder first).
func spreadOp(op MOOperation, from, to time.Time) map[string]int64 {
	out := map[string]int64{}
	start := midnightUTC(op.ScheduledStart)
	end := midnightUTC(op.ScheduledEnd)
	days := int(end.Sub(start).Hours() / 24)
	if days < 1 {
		days = 1
	}
	base := op.PlannedMinutes / int64(days)
	rem := op.PlannedMinutes % int64(days)
	for k := 0; k < days; k++ {
		day := start.AddDate(0, 0, k)
		if day.Before(midnightUTC(from)) || !day.Before(midnightUTC(to).AddDate(0, 0, 1)) {
			continue
		}
		slice := base
		if int64(k) < rem {
			slice++
		}
		out[dayKey(day)] += slice
	}
	return out
}

// CapacityView builds the per-day load vs capacity over [from, to] inclusive
// from scheduled operations. Multi-day operations spread evenly across the
// days they occupy (same rule as the scheduler); a day whose load exceeds
// capacity reports Overloaded.
func CapacityView(ops []MOOperation, workstationID int64, capacityMin int64, from, to time.Time) []CapacityDay {
	if capacityMin <= 0 {
		return nil
	}
	loads := map[string]int64{}
	for _, op := range ops {
		if op.WorkstationID != workstationID || op.Status == MOOpCanceled {
			continue
		}
		for d, v := range spreadOp(op, from, to) {
			loads[d] += v
		}
	}
	var out []CapacityDay
	for d := midnightUTC(from); !d.After(midnightUTC(to)); d = d.AddDate(0, 0, 1) {
		key := dayKey(d)
		load := loads[key]
		out = append(out, CapacityDay{
			Date:            key,
			WorkstationID:   workstationID,
			LoadMinutes:     load,
			CapacityMinutes: capacityMin,
			Overloaded:      load > capacityMin,
		})
	}
	return out
}
