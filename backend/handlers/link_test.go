package handlers

import (
	"testing"
	"time"

	"github.com/fintrak/backend/models"
	"github.com/stretchr/testify/assert"
)

func TestCalculateTransferScore(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name      string
		debitTxn  models.Transaction
		creditTxn models.Transaction
		minScore  float64
		maxScore  float64
	}{
		{
			name: "perfect match",
			debitTxn: models.Transaction{
				Amount:      1000,
				Date:        now,
				Description: "Transfer to Savings",
			},
			creditTxn: models.Transaction{
				Amount:      1000,
				Date:        now,
				Description: "Transfer from Checking",
			},
			minScore: 95,
			maxScore: 100,
		},
		{
			name: "date difference",
			debitTxn: models.Transaction{
				Amount:      1000,
				Date:        now,
				Description: "Rent",
			},
			creditTxn: models.Transaction{
				Amount:      1000,
				Date:        now.Add(48 * time.Hour), // 2 days later
				Description: "Rent",
			},
			minScore: 80,
			maxScore: 95,
		},
		{
			name: "keyword boost",
			debitTxn: models.Transaction{
				Amount:      500,
				Date:        now,
				Description: "UPI-PAY-123",
			},
			creditTxn: models.Transaction{
				Amount:      500,
				Date:        now,
				Description: "UPI-REC-123",
			},
			minScore: 90,
			maxScore: 100,
		},
		{
			name: "amount mismatch",
			debitTxn: models.Transaction{
				Amount: 1000,
				Date:   now,
			},
			creditTxn: models.Transaction{
				Amount: 1050, // 50 diff
				Date:   now,
			},
			minScore: 0,
			maxScore: 60,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := calculateTransferScore(tt.debitTxn, tt.creditTxn)
			assert.GreaterOrEqual(t, score, tt.minScore)
			assert.LessOrEqual(t, score, tt.maxScore)
		})
	}
}
