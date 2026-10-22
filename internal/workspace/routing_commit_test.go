package workspace

import (
	"database/sql"
	"testing"
	"time"
)

func TestRoutedFinalCommitRechecksRequestStartFreshness(t *testing.T) {
	for _, clockCase := range []string{"elapsed-expired", "wall-rollback"} {
		t.Run(clockCase, func(t *testing.T) {
			_, root, b, c, _, _, runtime := routingFixtures(t)
			if _, e := c.db.Exec(`CREATE TABLE route_commit_effect(value TEXT NOT NULL)`); e != nil {
				t.Fatal(e)
			}
			_, e := runtime.RouteTransaction(c.ctx, root.ID, b.self.Host, c.self.Host, RouteMessage, func(tx *sql.Tx, _ RouteAccess) error {
				if _, e := tx.Exec(`INSERT INTO route_commit_effect(value) VALUES('no expired admission')`); e != nil {
					return e
				}
				if clockCase == "elapsed-expired" {
					c.clock.advance(root.Policy.MembershipTTL+time.Second, root.Policy.MembershipTTL+time.Second)
				} else {
					c.clock.advance(-time.Hour, 0)
				}
				return nil
			})
			if e == nil {
				t.Fatal("current admission committed after its request-start membership validity/clock became unusable")
			}
			var count int
			if e = c.db.QueryRow(`SELECT count(*) FROM route_commit_effect`).Scan(&count); e != nil || count != 0 {
				t.Fatal("expired/rollback callback escaped atomic rollback", count, e)
			}
		})
	}
}
