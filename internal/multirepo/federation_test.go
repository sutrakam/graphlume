package multirepo

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/sutrakam/graphlume/internal/store"
)

func TestWorkspaceFederationLifecycleAndMatching(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "workspace.db")

	ctx := context.Background()
	db, err := store.InitWorkspaceDB(ctx, dbPath)
	if err != nil {
		t.Fatalf("init workspace db: %v", err)
	}
	defer db.Close()

	mgr := NewWorkspaceManager(db)

	// Register Payment Service (provides POST /v1/payments/{id}, writes kafka:payment.completed, writes table payments)
	paySvc := WorkspaceService{
		ServiceID: "payment-service",
		RelPath:   "services/payment",
		Role:      "service",
		DBPath:    "/db/payment.db",
	}
	payContracts := []ExportedContract{
		{
			Direction:    "provides",
			ContractKind: "http_route",
			CanonicalKey: "POST /v1/payments/{id}",
			SymbolFQN:    "com.pay.ChargeHandler#charge",
			FilePath:     "routes.go",
			Line:         42,
			Confidence:   "high",
		},
		{
			Direction:    "writes",
			ContractKind: "queue_topic",
			CanonicalKey: "kafka:payment.completed",
			SymbolFQN:    "com.pay.Events#publishCompleted",
			FilePath:     "events.go",
			Line:         88,
			Confidence:   "high",
		},
		{
			Direction:    "writes",
			ContractKind: "db_table",
			CanonicalKey: "payments",
			SymbolFQN:    "PaymentEntity",
			FilePath:     "models.go",
			Line:         15,
			Confidence:   "high",
		},
	}
	if err := mgr.SyncService(ctx, paySvc, payContracts); err != nil {
		t.Fatalf("sync payment service: %v", err)
	}

	// Register Checkout Service (consumes POST /v1/payments/charge, reads table payments)
	checkoutSvc := WorkspaceService{
		ServiceID: "checkout-service",
		RelPath:   "services/checkout",
		Role:      "service",
		DBPath:    "/db/checkout.db",
	}
	checkoutContracts := []ExportedContract{
		{
			Direction:    "consumes",
			ContractKind: "http_route",
			CanonicalKey: "POST /v1/payments/charge",
			SymbolFQN:    "CheckoutClient.charge",
			FilePath:     "client.ts",
			Line:         24,
			Confidence:   "high",
		},
		{
			Direction:    "reads",
			ContractKind: "db_table",
			CanonicalKey: "payments",
			SymbolFQN:    "PaymentRecord",
			FilePath:     "schema.ts",
			Line:         10,
			Confidence:   "medium",
		},
	}
	if err := mgr.SyncService(ctx, checkoutSvc, checkoutContracts); err != nil {
		t.Fatalf("sync checkout service: %v", err)
	}

	// Register Ledger Service (consumes kafka:payment.completed)
	ledgerSvc := WorkspaceService{
		ServiceID: "ledger-service",
		RelPath:   "services/ledger",
		Role:      "service",
		DBPath:    "/db/ledger.db",
	}
	ledgerContracts := []ExportedContract{
		{
			Direction:    "consumes",
			ContractKind: "queue_topic",
			CanonicalKey: "kafka:payment.completed",
			SymbolFQN:    "LedgerConsumer.handlePayment",
			FilePath:     "consumer.py",
			Line:         19,
			Confidence:   "high",
		},
	}
	if err := mgr.SyncService(ctx, ledgerSvc, ledgerContracts); err != nil {
		t.Fatalf("sync ledger service: %v", err)
	}

	// Check Upstream links to Payment Service's HTTP route
	upPage, err := mgr.FindUpstreamLinks(ctx, "payment-service", "POST /v1/payments/{id}", 0)
	upLinks := upPage.Links
	if err != nil {
		t.Fatalf("find upstream links: %v", err)
	}
	if len(upLinks) == 0 {
		t.Fatalf("expected upstream call from checkout-service to payment-service")
	}
	if upLinks[0].SrcServiceID != "checkout-service" || upLinks[0].LinkKind != "http_call" {
		t.Errorf("unexpected link: %+v", upLinks[0])
	}
	if upLinks[0].MatchRule != "normalized_route_template" {
		t.Errorf("match rule = %s, want normalized_route_template", upLinks[0].MatchRule)
	}

	// Check Downstream links from Payment Service
	downPage, err := mgr.FindDownstreamLinks(ctx, "payment-service", "", 0)
	downLinks := downPage.Links
	if err != nil {
		t.Fatalf("find downstream links: %v", err)
	}
	foundKafka := false
	foundSharedTable := false
	for _, l := range downLinks {
		if l.DstServiceID == "ledger-service" && l.LinkKind == "event_flow" {
			foundKafka = true
		}
		if l.DstServiceID == "checkout-service" && l.LinkKind == "shared_table" {
			foundSharedTable = true
		}
	}
	if !foundKafka {
		t.Errorf("expected kafka event_flow link to ledger-service")
	}
	if !foundSharedTable {
		t.Errorf("expected shared_table link with checkout-service")
	}
}
