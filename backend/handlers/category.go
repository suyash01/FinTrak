package handlers

import (
	"log"
	"net/http"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/db"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
)

func GetCategories(c *gin.Context) {
	rows, err := db.Pool.Query(c, "SELECT id, name, icon, color, parent_id, type FROM categories WHERE user_id = $1 ORDER BY type, name", auth.GetUserID(c))
	if err != nil {
		log.Printf("Error in GetCategories: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	defer rows.Close()

	categories := []models.Category{}
	for rows.Next() {
		var cat models.Category
		if err := rows.Scan(&cat.ID, &cat.Name, &cat.Icon, &cat.Color, &cat.ParentID, &cat.Type); err != nil {
			log.Printf("Error in GetCategories scan: %v\n", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
			return
		}
		categories = append(categories, cat)
	}

	c.JSON(http.StatusOK, categories)
}

func CreateCategory(c *gin.Context) {
	var cat models.Category
	if err := c.ShouldBindJSON(&cat); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	err := db.Pool.QueryRow(c,
		`INSERT INTO categories (user_id, name, icon, color, parent_id, type) VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, name, icon, color, parent_id, type`,
		auth.GetUserID(c), cat.Name, cat.Icon, cat.Color, cat.ParentID, cat.Type,
	).Scan(&cat.ID, &cat.Name, &cat.Icon, &cat.Color, &cat.ParentID, &cat.Type)

	if err != nil {
		log.Printf("Error in CreateCategory: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	c.JSON(http.StatusCreated, cat)
}
