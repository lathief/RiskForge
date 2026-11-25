package application

import "testing"

// BenchmarkRequestHash is the cost of the canonical hash an Idempotency-Key is tied to (RF-024).
func BenchmarkRequestHash(b *testing.B) {
	req := Request{ApplicationID: "LOAN-APP-DEMO-0001", ProductCode: "KTA_DEMO", Applicant: map[string]any{
		"age_years": float64(29), "monthly_income": "12000000.00", "existing_installment": "2500000.00",
		"employment_tenure_months": float64(36), "slik_max_kol": float64(1), "is_blacklisted": false,
	}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := RequestHash(req); err != nil {
			b.Fatal(err)
		}
	}
}
