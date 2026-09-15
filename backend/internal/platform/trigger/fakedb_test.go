package trigger

// Offline ferp_outbox fake: implements platform.DBTX with in-memory table
// semantics (pending filter, id ordering, delivered marking) so the
// emit → kill-simulation → relay → exactly-once flow runs without Postgres.
// Real SQL is covered by the pgtest-gated outbox_pg_test.go in CI.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type fakeOutboxRow struct {
	id, entityID, objectID int64
	name, subject          string
	payload                map[string]any
	delivered              bool
}

type fakeDB struct {
	mu   sync.Mutex
	seq  int64
	rows []*fakeOutboxRow
}

func newFakeDB() *fakeDB { return &fakeDB{} }

func (f *fakeDB) pending() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.rows {
		if !r.delivered {
			n++
		}
	}
	return n
}

func (f *fakeDB) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if strings.Contains(sql, "UPDATE ferp_outbox") {
		ids, ok := args[0].([]int64)
		if !ok {
			return pgconn.CommandTag{}, fmt.Errorf("fakeDB: UPDATE ids want []int64, got %T", args[0])
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, id := range ids {
			for _, r := range f.rows {
				if r.id == id {
					r.delivered = true
				}
			}
		}
		return pgconn.NewCommandTag(fmt.Sprintf("UPDATE %d", len(ids))), nil
	}
	return pgconn.CommandTag{}, fmt.Errorf("fakeDB: unexpected Exec: %s", sql)
}

func (f *fakeDB) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	if !strings.Contains(sql, "FROM ferp_outbox") {
		return nil, fmt.Errorf("fakeDB: unexpected Query: %s", sql)
	}
	limit := -1
	if len(args) > 0 {
		switch n := args[0].(type) {
		case int:
			limit = n
		case int64:
			limit = int(n)
		case int32:
			limit = int(n)
		default:
			return nil, fmt.Errorf("fakeDB: LIMIT want int, got %T", args[0])
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out [][]any
	for _, r := range f.rows {
		if r.delivered {
			continue
		}
		raw, err := json.Marshal(r.payload)
		if err != nil {
			return nil, err
		}
		out = append(out, []any{r.id, r.name, r.subject, r.entityID, r.objectID, raw})
		if limit >= 0 && len(out) >= limit {
			break
		}
	}
	return &fakeRows{rows: out}, nil
}

func (f *fakeDB) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	if !strings.Contains(sql, "INSERT INTO ferp_outbox") {
		return &fakeRow{err: fmt.Errorf("fakeDB: unexpected QueryRow: %s", sql)}
	}
	name, _ := args[0].(string)
	subject, _ := args[1].(string)
	entityID := asInt64(args[2])
	objectID := asInt64(args[3])
	var payload map[string]any
	switch raw := args[4].(type) {
	case []byte:
		if err := json.Unmarshal(raw, &payload); err != nil {
			return &fakeRow{err: err}
		}
	default:
		return &fakeRow{err: fmt.Errorf("fakeDB: payload want []byte, got %T", args[4])}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	f.rows = append(f.rows, &fakeOutboxRow{
		id: f.seq, entityID: entityID, objectID: objectID,
		name: name, subject: subject, payload: payload,
	})
	return &fakeRow{id: f.seq}
}

type fakeRow struct {
	id  int64
	err error
}

func (r *fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != 1 {
		return fmt.Errorf("fakeDB: QueryRow scan wants 1 dest, got %d", len(dest))
	}
	p, ok := dest[0].(*int64)
	if !ok {
		return fmt.Errorf("fakeDB: QueryRow scan wants *int64, got %T", dest[0])
	}
	*p = r.id
	return nil
}

type fakeRows struct {
	rows [][]any
	pos  int
	cur  []any
}

func (r *fakeRows) Close()                                       {}
func (r *fakeRows) Err() error                                   { return nil }
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Values() ([]any, error)                       { return r.cur, nil }
func (r *fakeRows) RawValues() [][]byte                          { return nil }
func (r *fakeRows) Conn() *pgx.Conn                              { return nil }

func (r *fakeRows) Next() bool {
	if r.pos >= len(r.rows) {
		r.cur = nil
		return false
	}
	r.cur = r.rows[r.pos]
	r.pos++
	return true
}

func (r *fakeRows) Scan(dest ...any) error {
	if r.cur == nil {
		return fmt.Errorf("fakeDB: Scan with no current row")
	}
	if len(dest) != len(r.cur) {
		return fmt.Errorf("fakeDB: Scan wants %d dests, got %d", len(r.cur), len(dest))
	}
	for i, d := range r.cur {
		switch p := dest[i].(type) {
		case *int64:
			*p = asInt64(d)
		case *string:
			s, _ := d.(string)
			*p = s
		case *[]byte:
			b, _ := d.([]byte)
			*p = b
		default:
			return fmt.Errorf("fakeDB: Scan dest %d unsupported %T", i, dest[i])
		}
	}
	return nil
}
