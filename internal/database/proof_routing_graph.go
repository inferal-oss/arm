package database

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"time"

	"getarm.org/internal/routing"
)

var routingGraphResetTables = []string{"routing_graph_issuance", "routing_graph_reports", "routing_graph_observations"}
var routingGraphResetSchema = []string{
	`CREATE TABLE routing_graph_issuance(workspace TEXT PRIMARY KEY,data TEXT NOT NULL)`,
	`CREATE TABLE routing_graph_reports(workspace TEXT NOT NULL,source TEXT NOT NULL,data TEXT NOT NULL,PRIMARY KEY(workspace,source))`,
	`CREATE TABLE routing_graph_observations(workspace TEXT PRIMARY KEY,data TEXT NOT NULL)`,
}

func graphProofIdentity(i proofWSIdentity) bool {
	if !sshProofHex(i.Host, 16) || !routeProofText(i.Name, 128) || !sshProofHex(i.Fingerprint, 32) || !sshProofHex(i.SPKI, 32) || len(i.Certificate) > 8192 {
		return false
	}
	b, rest := pem.Decode([]byte(i.Certificate))
	if b == nil || b.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		return false
	}
	c, e := x509.ParseCertificate(b.Bytes)
	if e != nil {
		return false
	}
	if c.IsCA || c.PublicKeyAlgorithm != x509.Ed25519 || c.KeyUsage != x509.KeyUsageDigitalSignature || len(c.DNSNames) != 1 || c.DNSNames[0] != "node-"+i.Host+".arm.invalid" || len(c.ExtKeyUsage) != 2 || !c.NotAfter.After(c.NotBefore) || !c.NotAfter.Equal(i.Expires) || !bytes.Equal(c.RawIssuer, c.RawSubject) {
		return false
	}
	if !(c.ExtKeyUsage[0] == x509.ExtKeyUsageClientAuth && c.ExtKeyUsage[1] == x509.ExtKeyUsageServerAuth || c.ExtKeyUsage[1] == x509.ExtKeyUsageClientAuth && c.ExtKeyUsage[0] == x509.ExtKeyUsageServerAuth) {
		return false
	}
	if c.CheckSignature(c.SignatureAlgorithm, c.RawTBSCertificate, c.Signature) != nil {
		return false
	}
	cert, key := sha256.Sum256(c.Raw), sha256.Sum256(c.RawSubjectPublicKeyInfo)
	return hex.EncodeToString(cert[:]) == i.Fingerprint && hex.EncodeToString(key[:]) == i.SPKI
}
func graphProofReport(q proofGraphReport) bool {
	raw, e := json.Marshal(q)
	if e != nil || len(raw) > 32<<10 || q.Schema != 1 || !sshProofHex(q.Workspace, 16) || !sshProofHex(q.Generation, 16) || !sshProofHex(q.Source, 16) || q.SourceGeneration < 1 || !sshProofHex(q.SourcePin, 32) || q.Sequence < 1 || !sshProofHex(q.Nonce, 32) || q.Issued.IsZero() || !q.ValidUntil.After(q.Issued) || q.ValidUntil.Sub(q.Issued) > time.Minute || q.Neighbors == nil || len(q.Neighbors) > 64 {
		return false
	}
	previous := ""
	for _, n := range q.Neighbors {
		if !sshProofHex(n.Host, 16) || n.Host == q.Source || n.Host <= previous || n.Generation < 1 || !sshProofHex(n.Pin, 32) || !sshProofHex(n.Pair, 32) {
			return false
		}
		previous = n.Host
	}
	return true
}
func graphProofBundle(b proofGraphBundle) error {
	s, m := b.Snapshot, b.Membership
	raw, e := json.Marshal(b)
	if e != nil || len(raw) > 1<<20 {
		return fmt.Errorf("complete graph bundle exceeds bound")
	}
	copy := m
	copy.Hash = ""
	p := m.Policy
	if m.Schema != 1 || !sshProofHex(m.Workspace, 16) || !sshProofHex(m.Generation, 16) || !sshProofHex(m.Requester, 16) || m.RequesterGeneration < 1 || !sshProofHex(m.Nonce, 32) || m.Revision < 1 || m.PolicyRevision < 1 || m.Issued.IsZero() || !m.ValidUntil.After(m.Issued) || m.TTL != m.ValidUntil.Sub(m.Issued) || m.TTL > p.MembershipTTL || m.Hash != workspaceProofHash(copy) || !graphProofIdentity(m.Directory) || len(m.Members) < 1 || len(m.Members) > 128 {
		return fmt.Errorf("graph retained membership structure/digest differs")
	}
	if p.MaxMembers < 1 || p.MaxMembers > 128 || p.MaxPublishedAgents < 0 || p.MaxPublishedAgents > 32 || p.MembershipTTL <= 0 || p.MembershipTTL > time.Minute || p.StreamTTL <= 0 || p.StreamTTL > 30*time.Second || p.StreamTTL > p.MembershipTTL || p.RefreshInterval <= 0 || p.RefreshInterval > 15*time.Second || p.RefreshInterval > p.MembershipTTL || p.ClockSkew <= 0 || p.ClockSkew > 5*time.Second || p.AdmissionMode != "delegated_introducers" {
		return fmt.Errorf("graph membership policy outside approved bounds")
	}
	members := map[string]proofWSMemberCard{}
	active := 0
	for _, c := range m.Members {
		if _, ok := members[c.Identity.Host]; ok {
			return fmt.Errorf("duplicate graph membership card")
		}
		if !graphProofIdentity(c.Identity) || c.MembershipGeneration < 1 || c.Revision < 1 || !oneOf(c.State, "active", "revoked") {
			return fmt.Errorf("invalid historical graph membership card")
		}
		if c.State == "active" {
			active++
		}
		members[c.Identity.Host] = c
	}
	if active > p.MaxMembers || members[m.Directory.Host].Identity != m.Directory || members[m.Directory.Host].State != "active" || members[m.Requester].State != "active" || members[m.Requester].MembershipGeneration != m.RequesterGeneration {
		return fmt.Errorf("graph directory/requester/active capacity differs")
	}
	graphCopy := s
	graphCopy.Hash = ""
	if s.Schema != 1 || s.Workspace != m.Workspace || s.Generation != m.Generation || s.Directory != m.Directory.Host || s.DirectoryPin != m.Directory.Fingerprint || s.MembershipHash != m.Hash || s.MembershipRevision != m.Revision || s.Requester != m.Requester || s.RequesterGeneration != m.RequesterGeneration || s.Nonce != m.Nonce || s.Revision < 1 || s.Issued.Before(m.Issued) || !s.ValidUntil.After(s.Issued) || s.ValidUntil.Sub(s.Issued) > time.Minute || s.ValidUntil.After(m.ValidUntil) || s.Hash != workspaceProofHash(graphCopy) || len(s.Nodes) != active || s.Edges == nil || len(s.Edges) > 4096 {
		return fmt.Errorf("graph topology differs from exact complete membership")
	}
	nodes := map[string]bool{}
	previous := ""
	for _, n := range s.Nodes {
		c, ok := members[n.Host]
		if !ok || c.State != "active" || n.Host <= previous || n.Generation != c.MembershipGeneration || n.Pin != c.Identity.Fingerprint || n.Relay && !p.Relay {
			return fmt.Errorf("graph node widens membership/relay policy")
		}
		nodes[n.Host] = true
		previous = n.Host
	}
	previous = ""
	for _, edge := range s.Edges {
		key := edge.A + ":" + edge.B
		if edge.A >= edge.B || !nodes[edge.A] || !nodes[edge.B] || !sshProofHex(edge.Pair, 32) || key <= previous {
			return fmt.Errorf("graph edge is unknown/noncanonical/duplicated")
		}
		previous = key
	}
	return nil
}
func (p *proofReader) readRoutingGraph(roots map[string]proofRouteRoot, parents map[string]proofWSInstance, changes []proofRouteChange, producer *routing.Records, bytesRead int) error {
	present := 0
	for _, table := range routingGraphResetTables {
		if _, ok := p.catalog[table]; ok {
			present++
		}
	}
	enables := map[string]proofRouteChange{}
	for _, c := range changes {
		if c.Kind == "graph_enabled" {
			if _, ok := enables[c.Workspace]; ok {
				return fmt.Errorf("graph enabled twice")
			}
			enables[c.Workspace] = c
		}
	}
	if !p.routingGraphSchema && present == 0 {
		if len(enables) > 0 {
			return fmt.Errorf("graph-enabled history lost dependencies")
		}
		return nil
	}
	if !p.routingGraphSchema || present != 3 {
		return fmt.Errorf("graph tables/marker are incomplete")
	}
	issuers := map[string]proofGraphIssuance{}
	reports := []proofGraphStoredReport{}
	observations := []proofGraphObservation{}
	producer.Graph = &routing.GraphRecords{Issuance: []routing.GraphIssuance{}, Reports: []routing.GraphReport{}, Observations: []routing.GraphObservation{}}
	for i, table := range routingGraphResetTables {
		count, limit := 0, 8
		if i == 1 {
			limit = 8 * 128
		}
		if e := p.walk(table, func(row proofRow) error {
			count++
			if count > limit {
				return fmt.Errorf("graph proof row bound exceeded")
			}
			raw, e := row.text("data")
			if e != nil {
				return e
			}
			bytesRead += len(raw)
			maxBytes := 1 << 20
			if i == 1 {
				maxBytes = 64 << 10
			}
			if len(raw) > maxBytes || bytesRead > 32<<20 {
				return fmt.Errorf("complete routing/graph byte bound exceeded")
			}
			ws, e := row.text("workspace")
			if e != nil {
				return e
			}
			switch i {
			case 0:
				var g proofGraphIssuance
				if e = workspaceProofJSON(raw, &g); e != nil {
					return e
				}
				if g.Workspace != ws {
					return fmt.Errorf("graph issuer key differs")
				}
				if _, ok := issuers[ws]; ok {
					return fmt.Errorf("duplicate graph issuer")
				}
				issuers[ws] = g
				var v routing.GraphIssuance
				if e = json.Unmarshal([]byte(raw), &v); e != nil {
					return e
				}
				producer.Graph.Issuance = append(producer.Graph.Issuance, v)
			case 1:
				var r proofGraphStoredReport
				if e = workspaceProofJSON(raw, &r); e != nil {
					return e
				}
				source, e := row.text("source")
				if e != nil || r.Workspace != ws || r.Report.Source != source {
					return fmt.Errorf("graph report key differs")
				}
				reports = append(reports, r)
				var v routing.GraphReport
				if e = json.Unmarshal([]byte(raw), &v); e != nil {
					return e
				}
				producer.Graph.Reports = append(producer.Graph.Reports, v)
			case 2:
				var o proofGraphObservation
				if e = workspaceProofJSON(raw, &o); e != nil {
					return e
				}
				if o.Workspace != ws {
					return fmt.Errorf("graph observation key differs")
				}
				observations = append(observations, o)
				var v routing.GraphObservation
				if e = json.Unmarshal([]byte(raw), &v); e != nil {
					return e
				}
				producer.Graph.Observations = append(producer.Graph.Observations, v)
			}
			return nil
		}); e != nil {
			return e
		}
	}
	if len(issuers) != len(enables) {
		return fmt.Errorf("graph enablement dependency missing")
	}
	for id, g := range issuers {
		r, ok := roots[id]
		parent := parents[id]
		c, enabled := enables[id]
		if !ok || !enabled || g.Schema != 1 || g.Workspace != id || g.Generation != r.Generation || g.Authority != r.Authority || g.LocalPin != r.LocalPin || g.Role != parent.Role || !oneOf(g.Role, "directory", "member") || g.EnableRevision != c.After.Revision || g.EnabledAt != c.After.Updated || g.Updated.Before(g.EnabledAt) || g.WallFloor.Before(g.Updated) || g.ReportSequence < 0 || g.IssuedCount < 0 || g.Closed != (r.State == "retired") {
			return fmt.Errorf("graph issuer immutable authority/enable/floor/closure differs")
		}
		if (g.LastReport == nil) != (g.ReportSequence == 0) || (g.LastReport == nil) != (g.ReportHash == "") {
			return fmt.Errorf("graph report highwater incomplete")
		}
		if q := g.LastReport; q != nil {
			if !graphProofReport(*q) || q.Workspace != id || q.Generation != g.Generation || q.Source != g.Authority.Host || q.SourcePin != g.LocalPin || q.Sequence != g.ReportSequence || g.ReportHash != workspaceProofHash(q) || q.Issued.After(g.WallFloor) {
				return fmt.Errorf("graph source report highwater differs")
			}
		}
		if g.Role == "member" && (g.LastIssue != nil || g.IssuedCount != 0) || (g.LastIssue == nil) != (g.IssuedCount == 0) {
			return fmt.Errorf("graph directory issue highwater differs")
		}
		if b := g.LastIssue; b != nil {
			if e := graphProofBundle(*b); e != nil {
				return e
			}
			s := b.Snapshot
			if s.Workspace != id || s.Generation != g.Generation || s.Directory != g.Authority.Host || s.DirectoryPin != g.LocalPin || s.Revision != g.IssuedCount || s.Issued.After(g.WallFloor) {
				return fmt.Errorf("graph issued bundle/native highwater differs")
			}
		}
		if g.ReportRefs == nil || len(g.ReportRefs) > 128 || g.Role == "member" && len(g.ReportRefs) > 0 {
			return fmt.Errorf("graph mandatory source inventory missing or outside role")
		}
		previous := ""
		for _, ref := range g.ReportRefs {
			if !sshProofHex(ref.Source, 16) || ref.Source <= previous || ref.Sequence < 1 || !sshProofHex(ref.Hash, 32) {
				return fmt.Errorf("graph source inventory noncanonical/duplicate/malformed")
			}
			previous = ref.Source
		}
		if g.Role == "directory" && g.IssuedCount > 0 && g.LastReport == nil {
			return fmt.Errorf("directory graph issue lost own source highwater")
		}

	}
	seen := map[string]bool{}
	for _, r := range reports {
		g, ok := issuers[r.Workspace]
		q := r.Report
		key := r.Workspace + ":" + q.Source
		if !ok || seen[key] || g.Role != "directory" || r.Schema != 1 || r.Authority != g.Authority || r.DirectoryPin != g.LocalPin || !graphProofReport(q) || q.Workspace != g.Workspace || q.Generation != g.Generation || r.Hash != workspaceProofHash(q) || r.Received.Before(g.EnabledAt) || r.Received.After(g.WallFloor) || !r.OriginalUntil.After(r.Received) || r.OriginalUntil.After(q.ValidUntil) || r.OriginalUntil.After(r.Received.Add(time.Minute)) || g.LastIssue == nil {
			return fmt.Errorf("graph stored source evidence differs")
		}
		found := false
		for _, c := range g.LastIssue.Membership.Members {
			if c.Identity.Host == q.Source && c.Identity.Fingerprint == q.SourcePin && c.MembershipGeneration == q.SourceGeneration && !r.OriginalUntil.After(c.Identity.Expires) {
				found = true
			}
		}
		if !found || q.Source == g.Authority.Host && (g.LastReport == nil || r.Hash != g.ReportHash) {
			return fmt.Errorf("graph source lost exact membership/highwater")
		}
		seen[key] = true
	}
	for _, g := range issuers {
		count := 0
		for _, r := range reports {
			if r.Workspace == g.Workspace {
				count++
			}
		}
		if count != len(g.ReportRefs) {
			return fmt.Errorf("graph complete source inventory cardinality differs")
		}
		for _, ref := range g.ReportRefs {
			found := false
			for _, r := range reports {
				if r.Workspace == g.Workspace && r.Report.Source == ref.Source && r.Report.Sequence == ref.Sequence && r.Hash == ref.Hash {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("graph retained source highwater row deleted or changed")
			}
		}
		if g.Role == "directory" && g.LastIssue != nil {
			own, requester := false, false
			m := g.LastIssue.Membership
			for _, r := range reports {
				if r.Workspace != g.Workspace {
					continue
				}
				q := r.Report
				if q.Source == g.Authority.Host && q.Sequence == g.ReportSequence && r.Hash == g.ReportHash {
					own = true
				}
				if q.Source == m.Requester && q.SourceGeneration == m.RequesterGeneration && q.Nonce == m.Nonce {
					for _, c := range m.Members {
						if c.Identity.Host == m.Requester && c.Identity.Fingerprint == q.SourcePin {
							requester = true
						}
					}
				}
			}
			if !own || !requester {
				return fmt.Errorf("graph issue lost exact own/requester source report")
			}
		}
	}

	seen = map[string]bool{}
	for _, o := range observations {
		g, ok := issuers[o.Workspace]
		s, q := o.Bundle.Snapshot, o.SourceReport
		if !ok || seen[o.Workspace] || g.Role != "member" || o.Schema != 1 || o.Authority != g.Authority || o.LocalPin != g.LocalPin || o.Received.Before(g.EnabledAt) || o.WallFloor.Before(o.Received) || o.WallFloor.After(g.WallFloor) || o.RequestStart.IsZero() || o.RequestStart.After(o.Received) {
			return fmt.Errorf("graph historical observation/floor differs")
		}
		if e := graphProofBundle(o.Bundle); e != nil {
			return e
		}
		if s.Workspace != g.Workspace || s.Generation != g.Generation || s.Requester != g.Authority.Host || !s.ValidUntil.After(o.Received) || s.Issued.Before(o.RequestStart.Add(-o.Bundle.Membership.Policy.ClockSkew)) || !graphProofReport(q) || q.Workspace != g.Workspace || q.Generation != g.Generation || q.Source != g.Authority.Host || q.SourcePin != g.LocalPin || q.SourceGeneration != s.RequesterGeneration || q.Nonce != s.Nonce || q.Issued != o.RequestStart || q.Issued.After(o.Received) || !q.ValidUntil.After(o.Received) || q.Sequence > g.ReportSequence || q.Sequence == g.ReportSequence && workspaceProofHash(q) != g.ReportHash {
			return fmt.Errorf("graph observation exact original report/membership linkage differs")
		}
		if e := graphProofDirectoryProbes(o, g); e != nil {
			return e
		}
		seen[o.Workspace] = true
	}
	return nil
}
