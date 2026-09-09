package port

import (
	"context"
	"time"

	"github.com/mpyw/moneyforward-paypaysec-bridge-action/v3/internal/application/domain/asset"
)

// Ledger is the account the holdings are recorded in.
//
// Addressed entirely by name: whatever identifiers the service uses to tell one
// row from another are its own business, and there is no way for a use case to
// hold them that would not make it that service's.
type Ledger interface {
	SignIn(ctx context.Context) error

	// Recorded is what the ledger holds now.
	Recorded(ctx context.Context) ([]asset.Asset, error)

	Create(ctx context.Context, a asset.Asset) error
	Update(ctx context.Context, a asset.Asset) error
	Delete(ctx context.Context, name string) error
}

// Explainer is an optional extra on a [Ledger], asked — after a write that
// reported success turns out not to have taken effect — whether the service
// said anything about why.
//
// Optional because it is a diagnostic, not a verdict. MoneyForward answers a
// rejected write with 200 and the page re-rendered, and carries unrelated error
// blocks on every render, so what it says cannot decide anything. But it is
// where "名称は20文字以内でお願いします" comes from, and a failure without it is one
// somebody has to reproduce.
type Explainer interface {
	// LastRejection returns what the service said about the most recent write,
	// or "" if it said nothing.
	LastRejection() string
}

// EventuallyConsistent is an optional hint from a ledger whose reads can lag
// behind successful writes. The sync use case still verifies every write, but
// gives these ledgers time to expose it before declaring failure.
type EventuallyConsistent interface {
	ConfirmationAttempts() int
	ConfirmationDelay() time.Duration
}
