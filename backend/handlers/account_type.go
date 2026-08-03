package handlers

import (
	"errors"
	"log"
	"net/http"

	"github.com/fintrak/backend/db"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

func GetAccountTypes(c *gin.Context) {
	rows, err := db.Pool.Query(c, "SELECT id, name, positive_txn_type FROM account_types ORDER BY name")
	if err != nil {
		log.Printf("Error in GetAccountTypes: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	defer rows.Close()

	types := []models.AccountType{}
	for rows.Next() {
		var at models.AccountType
		if err := rows.Scan(&at.ID, &at.Name, &at.PositiveTxnType); err != nil {
			log.Printf("Error in GetAccountTypes scan: %v\n", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
			return
		}
		types = append(types, at)
	}

	c.JSON(http.StatusOK, types)
}

func CreateAccountType(c *gin.Context) {
	var req models.CreateAccountTypeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.PositiveTxnType != "credit" && req.PositiveTxnType != "debit" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "positiveTxnType must be 'credit' or 'debit'"})
		return
	}

	var at models.AccountType
	err := db.Pool.QueryRow(c,
		`INSERT INTO account_types (id, name, positive_txn_type) VALUES ($1, $2, $3)
		 RETURNING id, name, positive_txn_type`,
		req.ID, req.Name, req.PositiveTxnType,
	).Scan(&at.ID, &at.Name, &at.PositiveTxnType)

	if err != nil {
		log.Printf("Error in CreateAccountType: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	c.JSON(http.StatusCreated, at)
}

func UpdateAccountType(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var req models.UpdateAccountTypeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.PositiveTxnType != "" && req.PositiveTxnType != "credit" && req.PositiveTxnType != "debit" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "positiveTxnType must be 'credit' or 'debit'"})
		return
	}

	var at models.AccountType
	err := db.Pool.QueryRow(c,
		`UPDATE account_types SET name = COALESCE(NULLIF($1, ''), name), 
		 positive_txn_type = COALESCE(NULLIF($2, ''), positive_txn_type)
		 WHERE id = $3
		 RETURNING id, name, positive_txn_type`,
		req.Name, req.PositiveTxnType, id,
	).Scan(&at.ID, &at.Name, &at.PositiveTxnType)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "account type not found"})
			return
		}
		log.Printf("Error in UpdateAccountType: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	c.JSON(http.StatusOK, at)
}

func DeleteAccountType(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	// Check if any accounts are using this type
	var count int
	db.Pool.QueryRow(c, "SELECT COUNT(*) FROM accounts WHERE account_type_id = $1", id).Scan(&count)
	if count > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "cannot delete: account type is in use by existing accounts"})
		return
	}

	result, err := db.Pool.Exec(c, "DELETE FROM account_types WHERE id = $1", id)
	if err != nil {
		log.Printf("Error in DeleteAccountType: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	if result.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "account type not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}
