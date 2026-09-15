package trigger

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Emit writes one catalogue event to ferp_outbox inside the CALLER's
// transaction (db is the caller's tx or pool). It never publishes: the Relay
// delivers after commit, so a crash between commit and publish still
// delivers. Payload gains entity_id (the tenant rides in the payload —
// ferp_outbox is a global queue by design, see 0024) and entity when the
// definition carries one; the object id is read from payload "id".
func Emit(ctx context.Context, db platform.DBTX, def Definition, entityID int64, payload map[string]any) error {
	if db == nil {
		return fmt.Errorf("trigger: emit %s needs a db: %w", def.Name, platform.ErrValidation)
	}
	if err := def.Validate(); err != nil {
		return err
	}
	if entityID <= 0 {
		return fmt.Errorf("trigger: emit %s needs entity_id: %w", def.Name, platform.ErrValidation)
	}
	body := make(map[string]any, len(payload)+2)
	for k, v := range payload {
		body[k] = v
	}
	body["entity_id"] = entityID
	if def.Entity != "" {
		if _, ok := body["entity"]; !ok {
			body["entity"] = def.Entity
		}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("trigger: emit %s marshal: %w", def.Name, err)
	}
	var id int64
	err = db.QueryRow(ctx,
		`INSERT INTO ferp_outbox (event_name, subject, entity_id, object_id, payload)
		 VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		def.Name, def.Subject, entityID, asInt64(body["id"]), raw,
	).Scan(&id)
	if err != nil {
		return fmt.Errorf("trigger: emit %s insert: %w", def.Name, err)
	}
	return nil
}

// EmitEvent emits a typed event (object id defaults from the event when the
// payload omits "id").
func EmitEvent(ctx context.Context, db platform.DBTX, e Event) error {
	if e == nil {
		return fmt.Errorf("trigger: emit needs an event: %w", platform.ErrValidation)
	}
	payload := e.Payload()
	if payload == nil {
		payload = map[string]any{}
	}
	if _, ok := payload["id"]; !ok {
		cp := make(map[string]any, len(payload)+1)
		for k, v := range payload {
			cp[k] = v
		}
		cp["id"] = e.ObjectID()
		payload = cp
	}
	return Emit(ctx, db, e.Def(), e.EntityID(), payload)
}

func asInt64(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case int32:
		return int64(n)
	case float64:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	default:
		return 0
	}
}

// Relay delivers pending outbox rows to the bus. The caller drives it
// (per-tick RunOnce against the pool); no daemon is wired here.
//
// Crash safety: each run claims rows with SELECT ... FOR UPDATE SKIP LOCKED,
// publishes them, then marks delivered_at in the same transaction. Publish
// happens BEFORE the marking commit, so a crash between commit and publish
// cannot lose an event — the worst case is at-least-once redelivery after a
// crash between publish and commit, which handlers already tolerate via
// status guards + row versions. A failed publish aborts the run unmarked so
// the next tick retries.
type Relay struct {
	Bus platform.Bus
	// Batch caps rows per run (<=0 defaults to 100).
	Batch int
}

func (r *Relay) batchSize() int {
	if r == nil || r.Batch <= 0 {
		return 100
	}
	return r.Batch
}

// RunOnce delivers up to Batch pending rows, returning the delivered count.
// A *pgxpool.Pool runs in an owned transaction; any other DBTX (a pgx.Tx,
// a test fake) runs caller-scoped — SKIP LOCKED only isolates concurrent
// relays inside a real transaction.
func (r *Relay) RunOnce(ctx context.Context, db platform.DBTX) (int64, error) {
	if r == nil || r.Bus == nil {
		return 0, fmt.Errorf("trigger: relay needs a bus: %w", platform.ErrValidation)
	}
	if db == nil {
		return 0, fmt.Errorf("trigger: relay needs a db: %w", platform.ErrValidation)
	}
	if pool, ok := db.(*pgxpool.Pool); ok {
		if pool == nil {
			return 0, fmt.Errorf("trigger: relay needs a db: %w", platform.ErrValidation)
		}
		var n int64
		err := platform.Tx(ctx, pool, func(tx pgx.Tx) error {
			var err error
			n, err = r.drain(ctx, tx)
			return err
		})
		return n, err
	}
	return r.drain(ctx, db)
}

func (r *Relay) drain(ctx context.Context, db platform.DBTX) (int64, error) {
	rows, err := db.Query(ctx,
		`SELECT id, event_name, subject, entity_id, object_id, payload
		 FROM ferp_outbox WHERE delivered_at IS NULL ORDER BY id LIMIT $1
		 FOR UPDATE SKIP LOCKED`, r.batchSize())
	if err != nil {
		return 0, fmt.Errorf("trigger: relay claim: %w", err)
	}
	defer rows.Close()
	type pending struct {
		outboxID, entityID, objectID int64
		subject, entity              string
		payload                      map[string]any
	}
	var batch []pending
	for rows.Next() {
		var p pending
		var raw []byte
		var name string
		if err := rows.Scan(&p.outboxID, &name, &p.subject, &p.entityID, &p.objectID, &raw); err != nil {
			return 0, fmt.Errorf("trigger: relay scan: %w", err)
		}
		p.payload = map[string]any{}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &p.payload); err != nil {
				return 0, fmt.Errorf("trigger: relay payload %d: %w", p.outboxID, err)
			}
		}
		if s, _ := p.payload["entity"].(string); s != "" {
			p.entity = s
		}
		batch = append(batch, p)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("trigger: relay rows: %w", err)
	}
	if len(batch) == 0 {
		return 0, nil
	}
	ids := make([]int64, 0, len(batch))
	for _, p := range batch {
		e := platform.Event{
			Subject:  p.subject,
			Entity:   p.entity,
			EntityID: p.entityID,
			ID:       p.objectID,
			Payload:  p.payload,
		}
		if err := r.Bus.Publish(ctx, e); err != nil {
			return 0, fmt.Errorf("trigger: relay publish %s: %w", p.subject, err)
		}
		ids = append(ids, p.outboxID)
	}
	if _, err := db.Exec(ctx,
		`UPDATE ferp_outbox SET claimed_at = COALESCE(claimed_at, now()),
		 delivered_at = now() WHERE id = ANY($1)`, ids); err != nil {
		return 0, fmt.Errorf("trigger: relay mark sent: %w", err)
	}
	return int64(len(ids)), nil
}

// Subscribe wires fn to a catalogue event by name (webhook-style): the
// lookup hits the registry, the delivery rides the bus subject. A nil
// registry uses Default.
func Subscribe(reg *Registry, bus platform.Bus, name string, fn func(ctx context.Context, e platform.Event)) (func(), error) {
	if reg == nil {
		reg = Default()
	}
	if bus == nil {
		return nil, fmt.Errorf("trigger: subscribe %s needs a bus: %w", name, platform.ErrValidation)
	}
	def, err := reg.ByName(name)
	if err != nil {
		return nil, err
	}
	return bus.Subscribe(def.Subject, fn), nil
}
