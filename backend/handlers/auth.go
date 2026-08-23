package handlers

import (
	"errors"
	"log"
	"net/http"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/db"
	"github.com/fintrak/backend/internal/validation"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func Register(c *gin.Context) {
	var req models.RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		log.Printf("Error hashing password in Register: %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	var user models.User
	err = db.Pool.QueryRow(c,
		`INSERT INTO users (email, password_hash) VALUES ($1, $2)
		 RETURNING id, email`,
		req.Email, hash,
	).Scan(&user.ID, &user.Email)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			validation.RespondError(c, "an account with this email already exists", http.StatusConflict)
			return
		}
		log.Printf("Error in Register: %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	db.SeedDefaultCategories(c, user.ID)

	token, err := auth.GenerateToken(user.ID, c.MustGet("jwtSecret").(string))
	if err != nil {
		log.Printf("Error generating token in Register: %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusCreated, models.AuthResponse{Token: token, User: user})
}

func Login(c *gin.Context) {
	var req models.LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	var user models.User
	var passwordHash string
	err := db.Pool.QueryRow(c,
		"SELECT id, email, password_hash FROM users WHERE LOWER(email) = LOWER($1)",
		req.Email,
	).Scan(&user.ID, &user.Email, &passwordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		validation.RespondError(c, "invalid email or password", http.StatusUnauthorized)
		return
	}
	if err != nil {
		log.Printf("Error in Login (query): %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	if !auth.CheckPassword(passwordHash, req.Password) {
		validation.RespondError(c, "invalid email or password", http.StatusUnauthorized)
		return
	}

	token, err := auth.GenerateToken(user.ID, c.MustGet("jwtSecret").(string))
	if err != nil {
		log.Printf("Error generating token in Login: %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, models.AuthResponse{Token: token, User: user})
}
