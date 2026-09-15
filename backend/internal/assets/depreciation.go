package assets

import (
	"fmt"
	"strings"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Depreciation methods.
const (
	DepLinear     = "linear"
	DepDegressive = "degressive"
)

// Schedule status.
const (
	ScheduleActive int16 = 0
	ScheduleDone   int16 = 1
)

// AssetSchedule is one depreciation plan for an asset (one per asset).
// Cost basis lives here: ferp_assets carries no money column.
type AssetSchedule struct {
	ID            int64     `json:"id"`
	EntityID      int64     `json:"entity_id"`
	AssetID       int64     `json:"asset_id"`
	Method        string    `json:"method"`   // linear|degressive
	Cost          int64     `json:"cost"`     // minor units, > 0
	RateBps       int64     `json:"rate_bps"` // per-period bps for degressive (1..10000); ignored for linear
	StartDate     time.Time `json:"start_date"`
	Periods       int       `json:"periods"`
	PostedPeriods int       `json:"posted_periods"`
	Accumulated   int64     `json:"accumulated"` // sum posted so far, minor units
	Status        int16     `json:"status"`      // 0 active, 1 fully posted
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	RowVersion    int64     `json:"row_version"`
}

// Validate checks schedule invariants.
func (s AssetSchedule) Validate() error {
	if s.EntityID <= 0 || s.AssetID <= 0 {
		return fmt.Errorf("assets: entity_id and asset_id required: %w", platform.ErrValidation)
	}
	switch s.Method {
	case DepLinear:
	case DepDegressive:
		if s.RateBps <= 0 || s.RateBps > 10000 {
			return fmt.Errorf("assets: degressive rate_bps must be 1..10000: %w", platform.ErrValidation)
		}
	default:
		return fmt.Errorf("assets: unknown method (linear|degressive): %w", platform.ErrValidation)
	}
	if s.Cost <= 0 {
		return fmt.Errorf("assets: cost must be positive: %w", platform.ErrValidation)
	}
	if s.Periods <= 0 {
		return fmt.Errorf("assets: periods must be positive: %w", platform.ErrValidation)
	}
	if s.StartDate.IsZero() {
		return fmt.Errorf("assets: start_date required: %w", platform.ErrValidation)
	}
	if s.PostedPeriods < 0 || s.PostedPeriods > s.Periods {
		return fmt.Errorf("assets: posted_periods out of range: %w", platform.ErrValidation)
	}
	if s.Accumulated < 0 || s.Accumulated > s.Cost {
		return fmt.Errorf("assets: accumulated out of range: %w", platform.ErrValidation)
	}
	return nil
}

// NetBookValue returns cost minus accumulated depreciation.
func (s AssetSchedule) NetBookValue() int64 { return s.Cost - s.Accumulated }

// DepLine is one generated period amount (minor units).
type DepLine struct {
	Seq    int   `json:"seq"` // 1-based period
	Amount int64 `json:"amount"`
}

// BuildSchedule is the pure depreciation generator (int64 math only).
//
//   - linear: cost split evenly; the last period absorbs the remainder, so
//     the plan always sums to exactly cost.
//   - degressive (declining balance): each non-final period takes
//     half-up(remaining * rateBps / 10000) whole units; the final period
//     takes the remainder, so the plan always sums to exactly cost.
//     Whole-unit rounding note: a mid-plan period may compute to zero when
//     remaining * rate < half a unit (tiny book values with small rates);
//     zeros are kept as-is and the remainder lands on the final period.
//     rateBps is per-period basis points (1..10000); 10000 fully expenses
//     the remaining value in the first period.
func BuildSchedule(cost int64, method string, rateBps int64, periods int) ([]DepLine, error) {
	if cost <= 0 {
		return nil, fmt.Errorf("assets: cost must be positive: %w", platform.ErrValidation)
	}
	if periods <= 0 {
		return nil, fmt.Errorf("assets: periods must be positive: %w", platform.ErrValidation)
	}
	switch strings.ToLower(strings.TrimSpace(method)) {
	case DepLinear:
		out := make([]DepLine, 0, periods)
		base := cost / int64(periods)
		paid := int64(0)
		for i := 0; i < periods; i++ {
			amt := base
			if i == periods-1 {
				amt = cost - paid
			}
			out = append(out, DepLine{Seq: i + 1, Amount: amt})
			paid += amt
		}
		return out, nil
	case DepDegressive:
		if rateBps <= 0 || rateBps > 10000 {
			return nil, fmt.Errorf("assets: degressive rate_bps must be 1..10000: %w", platform.ErrValidation)
		}
		out := make([]DepLine, 0, periods)
		remaining := cost
		for i := 0; i < periods; i++ {
			var amt int64
			if i == periods-1 {
				amt = remaining
			} else {
				amt = (remaining*rateBps + 5000) / 10000
				if amt > remaining {
					amt = remaining
				}
			}
			out = append(out, DepLine{Seq: i + 1, Amount: amt})
			remaining -= amt
		}
		return out, nil
	default:
		return nil, fmt.Errorf("assets: unknown method (linear|degressive): %w", platform.ErrValidation)
	}
}
