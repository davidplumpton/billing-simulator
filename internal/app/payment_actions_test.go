package app

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

func TestPaymentActionRegistryCoversSubmittedPaymentActions(t *testing.T) {
	t.Parallel()

	for _, action := range []string{
		"schedule",
		"process",
		"fail",
		"mark_due",
		"mark_past_due",
		"collect",
		"refund",
		"fix_method",
		"set_default_method",
		"set_default_profile",
	} {
		spec, ok := paymentActionSpecFor(action)
		if !ok {
			t.Fatalf("paymentActionSpecFor(%q) missing registry entry", action)
		}
		if spec.name != action {
			t.Fatalf("paymentActionSpecFor(%q) name = %q, want %q", action, spec.name, action)
		}
		if spec.apply == nil {
			t.Fatalf("paymentActionSpecFor(%q) has nil apply function", action)
		}
	}
}

func TestPaymentActionRunnerRejectsUnsupportedAction(t *testing.T) {
	t.Parallel()

	_, err := (paymentActionRunner{}).Apply(context.Background(), url.Values{
		"action": {"unknown"},
	})
	if err == nil || !strings.Contains(err.Error(), `unsupported payment action "unknown"`) {
		t.Fatalf("Apply(unknown) error = %v, want unsupported action", err)
	}
}

func TestPaymentActionRunnerRequiresClockForImplicitTransitionTimestamp(t *testing.T) {
	t.Parallel()

	_, err := (paymentActionRunner{}).transitionRequestFromValues(context.Background(), url.Values{
		"invoice_obligation_id": {"invobl_missing_clock"},
	}, false)
	if err == nil || !strings.Contains(err.Error(), "read simulator clock for payment lifecycle action") {
		t.Fatalf("transitionRequestFromValues(blank occurred_at) error = %v, want simulator clock error", err)
	}
}
