package handlers

import (
	"log"
	"net/http"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/db"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func GetPayees(c *gin.Context) {
	rows, err := db.Pool.Query(c, "SELECT id, name, account_id, created_at, updated_at FROM payees WHERE user_id = $1 ORDER BY name", auth.GetUserID(c))
	if err != nil {
		log.Printf("Error in GetPayees: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	defer rows.Close()

	payees := []models.Payee{}
	for rows.Next() {
		var p models.Payee
		if err := rows.Scan(&p.ID, &p.Name, &p.AccountID, &p.CreatedAt, &p.UpdatedAt); err != nil {
			log.Printf("Error in GetPayees scan: %v\n", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
			return
		}
		payees = append(payees, p)
	}

	c.JSON(http.StatusOK, payees)
}

func CreatePayee(c *gin.Context) {
	var req models.CreatePayeeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var p models.Payee
	err := db.Pool.QueryRow(c,
		"INSERT INTO payees (user_id, name, account_id) VALUES ($1, $2, $3) RETURNING id, name, account_id, created_at, updated_at",
		auth.GetUserID(c), req.Name, req.AccountID,
	).Scan(&p.ID, &p.Name, &p.AccountID, &p.CreatedAt, &p.UpdatedAt)

	if err != nil {
		log.Printf("Error in CreatePayee: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	c.JSON(http.StatusCreated, p)
}

func UpdatePayee(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var req models.CreatePayeeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var p models.Payee
	err = db.Pool.QueryRow(c,
		"UPDATE payees SET name = $1, account_id = $2, updated_at = NOW() WHERE id = $3 AND user_id = $4 RETURNING id, name, account_id, created_at, updated_at",
		req.Name, req.AccountID, id, auth.GetUserID(c),
	).Scan(&p.ID, &p.Name, &p.AccountID, &p.CreatedAt, &p.UpdatedAt)

	if err != nil {
		log.Printf("Error in UpdatePayee: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	c.JSON(http.StatusOK, p)
}

func DeletePayee(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	userID := auth.GetUserID(c)
	_, err = db.Pool.Exec(c, "DELETE FROM payees WHERE id = $1 AND user_id = $2", id, userID)
	if err != nil {
		log.Printf("Error in DeletePayee: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}
