package handlers

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/fintrak/backend/db"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func GetAccounts(c *gin.Context) {
	query := `
		SELECT a.id, a.name, a.type, a.bank, a.currency, a.color,
		COALESCE(SUM(CASE 
			WHEN a.type = 'bank' THEN (CASE WHEN t.type = 'credit' THEN t.amount ELSE -t.amount END)
			WHEN a.type = 'credit_card' THEN (CASE WHEN t.type = 'debit' THEN t.amount ELSE -t.amount END)
			ELSE 0 END), 0) as balance
		FROM accounts a
		LEFT JOIN transactions t ON a.id = t.account_id
		GROUP BY a.id, a.name, a.type, a.bank, a.currency, a.color
		ORDER BY a.created_at DESC`

	rows, err := db.Pool.Query(c, query)
	if err != nil {
		log.Printf("Error in GetAccounts: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	defer rows.Close()

	accounts := []models.Account{}
	for rows.Next() {
		var a models.Account
		if err := rows.Scan(&a.ID, &a.Name, &a.Type, &a.Bank, &a.Currency, &a.Color, &a.Balance); err != nil {
			log.Printf("Error in GetAccounts scan: %v\n", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
			return
		}
		accounts = append(accounts, a)
	}

	c.JSON(http.StatusOK, accounts)
}

func CreateAccount(c *gin.Context) {
	var req models.CreateAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Currency == "" {
		req.Currency = "INR"
	}
	if req.Color == "" {
		req.Color = "#06b6d4"
	}

	var account models.Account
	err := db.WithTx(c, func(tx pgx.Tx) error {
		err := tx.QueryRow(c,
			`INSERT INTO accounts (name, type, bank, currency, color) VALUES ($1, $2, $3, $4, $5)
			 RETURNING id, name, type, bank, currency, color`,
			req.Name, req.Type, req.Bank, req.Currency, req.Color,
		).Scan(&account.ID, &account.Name, &account.Type, &account.Bank, &account.Currency, &account.Color)

		if err != nil {
			return err
		}

		// Synchronize with Payees: Create a payee for the new account
		_, err = tx.Exec(c,
			`INSERT INTO payees (name, account_id) VALUES ($1, $2)
			 ON CONFLICT (account_id) DO UPDATE SET name = EXCLUDED.name`,
			account.Name, account.ID,
		)
		if err != nil {
			return fmt.Errorf("failed to create linked payee: %w", err)
		}

		return nil
	})

	if err != nil {
		log.Printf("Error in CreateAccount: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	account.Balance = 0 // New account has 0 balance
	c.JSON(http.StatusCreated, account)
}

func DeleteAccount(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	_, err = db.Pool.Exec(c, "DELETE FROM accounts WHERE id = $1", id)
	if err != nil {
		log.Printf("Error in DeleteAccount: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

func UpdateAccount(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var req models.UpdateAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var account models.Account
	err = db.WithTx(c, func(tx pgx.Tx) error {
		err := tx.QueryRow(c,
			`WITH updated AS (
				UPDATE accounts SET name = $1, type = $2, bank = $3, currency = $4, color = $5, updated_at = NOW() 
				WHERE id = $6 RETURNING id, name, type, bank, currency, color
			)
			SELECT u.id, u.name, u.type, u.bank, u.currency, u.color,
			COALESCE((SELECT SUM(CASE 
				WHEN u.type = 'bank' THEN (CASE WHEN t.type = 'credit' THEN t.amount ELSE -t.amount END)
				WHEN u.type = 'credit_card' THEN (CASE WHEN t.type = 'debit' THEN t.amount ELSE -t.amount END)
				ELSE 0 END) FROM transactions t WHERE t.account_id = u.id), 0) as balance
			FROM updated u`,
			req.Name, req.Type, req.Bank, req.Currency, req.Color, id,
		).Scan(&account.ID, &account.Name, &account.Type, &account.Bank, &account.Currency, &account.Color, &account.Balance)

		if err != nil {
			return err
		}

		// Synchronize with Payees: Update the corresponding payee name
		_, err = tx.Exec(c,
			`UPDATE payees SET name = $1 WHERE account_id = $2`,
			account.Name, account.ID,
		)
		if err != nil {
			return fmt.Errorf("failed to update linked payee: %w", err)
		}

		return nil
	})

	if err != nil {
		log.Printf("Error in UpdateAccount: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	c.JSON(http.StatusOK, account)
}

func ExportAccount(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	rows, err := db.Pool.Query(c, "SELECT date, description, amount, type, tags, notes FROM transactions WHERE account_id = $1 ORDER BY date DESC", id)
	if err != nil {
		log.Printf("Error in ExportAccount: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	defer rows.Close()

	c.Writer.Header().Set("Content-Type", "text/csv")
	c.Writer.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="account_%s_export.csv"`, id.String()[:8]))

	c.Writer.Write([]byte("Date,Description,Amount,Type,Tags,Notes\n"))

	for rows.Next() {
		var (
			date        time.Time
			description string
			amount      float64
			txnType     string
			tags        []string
			notes       string
		)
		if err := rows.Scan(&date, &description, &amount, &txnType, &tags, &notes); err != nil {
			continue
		}

		descEscaped := strings.ReplaceAll(description, `"`, `""`)
		notesEscaped := strings.ReplaceAll(notes, `"`, `""`)
		tagsStr := strings.Join(tags, ";")

		line := fmt.Sprintf("%s,\"%s\",%.2f,%s,\"%s\",\"%s\"\n",
			date.Format("2006-01-02"), descEscaped, amount, txnType, tagsStr, notesEscaped)
		c.Writer.Write([]byte(line))
	}
}
