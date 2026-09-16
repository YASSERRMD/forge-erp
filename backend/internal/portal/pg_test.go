package portal

import (
	"context"
	"testing"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
)

func TestPGPortalTokens(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()
	st := NewPGStore(pool)

	salt, _ := MintSalt()
	raw, _ := MintToken()
	row := &PortalToken{EntityID: 1, OrgID: 42, Salt: salt,
		TokenHash: HashToken(salt, raw), ExpiresAt: time.Now().UTC().Add(time.Hour)}
	if err := st.CreateToken(ctx, pool, row); err != nil {
		t.Fatalf("create: %v", err)
	}
	if row.ID == 0 {
		t.Fatal("ID unset")
	}
	got, err := st.TokenByHash(ctx, pool, row.TokenHash)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got.OrgID != 42 || !VerifyToken(got, raw) {
		t.Errorf("round-trip: %+v", got)
	}
	if err := st.RevokeToken(ctx, pool, 1, row.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	// Revoked tokens disappear from auth lookups.
	if _, err := st.TokenByHash(ctx, pool, row.TokenHash); err == nil {
		t.Fatal("revoked token still resolves")
	}
}
