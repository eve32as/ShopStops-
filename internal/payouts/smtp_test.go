package payouts

import (
	"strings"
	"testing"
	"time"
)

func TestNewSMTPSenderValidatesRequiredConfiguration(t *testing.T) {
	for _, config := range []SMTPConfig{
		{},
		{Address: "mail.example:587", Host: "mail.example", From: "bad\r\nBcc:x"},
		{Address: "mail.example:587", Host: "mail.example", From: "sender@example.com", Username: "user"},
	} {
		if _, err := NewSMTPSender(config); err == nil {
			t.Fatalf("expected invalid SMTP configuration error for %+v", config)
		}
	}
	sender, err := NewSMTPSender(SMTPConfig{
		Address: "mail.example:587", Host: "mail.example",
		From: "sender@example.com", Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if sender.config.Timeout != time.Second {
		t.Fatalf("timeout = %s, want 1s", sender.config.Timeout)
	}
}

func TestPayoutEmailIncludesPeriodAndEarnings(t *testing.T) {
	start := time.Date(2026, time.January, 5, 0, 0, 0, 0, time.UTC)
	message := payoutEmail("from@example.com", "driver@example.com", "<id@example.com>",
		WeeklyStatement{
			PeriodStart: start, PeriodEnd: start.AddDate(0, 0, 7),
			CompletedRoutes: 2, DistanceMeters: 4828,
			BasePayCents: 1400, MileagePayCents: 300, TotalEarningsCents: 1700,
		})
	for _, expected := range []string{
		"From: from@example.com",
		"To: driver@example.com",
		"Message-ID: <id@example.com>",
		"Jan 5, 2026 through Jan 11, 2026",
		"Completed routes: 2",
		"Base pay: $14.00",
		"Mileage pay: $3.00",
		"Total earnings: $17.00",
		"not a confirmation of funds transfer",
	} {
		if !strings.Contains(message, expected) {
			t.Errorf("statement email missing %q", expected)
		}
	}
}
