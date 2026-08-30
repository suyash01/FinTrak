package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fintrak/backend/db"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v3"
	"github.com/stretchr/testify/assert"
)

func newCategoryTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.GET("/categories", GetCategories)
	r.POST("/categories", CreateCategory)
	r.PUT("/categories/:id", UpdateCategory)
	r.DELETE("/categories/:id", DeleteCategory)
	return r
}

func TestGetCategories(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	r := newCategoryTestRouter()
	userID := testUserID()

	rows := pgxmock.NewRows([]string{"id", "name", "icon", "color", "group_id", "is_global", "group_name", "group_is_base"}).
		AddRow(uuid.New(), "Food & Dining", "utensils", "#f97316", "expense", false, "Expense", true).
		AddRow(uuid.New(), "Salary", "wallet", "#22c55e", "income", false, "Income", true)

	mock.ExpectQuery("SELECT c.id, c.name, c.icon, c.color, c.group_id").
		WithArgs(userID).
		WillReturnRows(rows)

	req, _ := http.NewRequest(http.MethodGet, "/categories", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var categories []models.Category
	err = json.Unmarshal(w.Body.Bytes(), &categories)
	assert.NoError(t, err)
	assert.Len(t, categories, 2)
	assert.Equal(t, "Food & Dining", categories[0].Name)
	assert.Equal(t, "expense", categories[0].GroupID)
	assert.False(t, categories[0].IsGlobal)
	assert.Equal(t, "income", categories[1].GroupID)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetCategoriesQueryError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	r := newCategoryTestRouter()

	mock.ExpectQuery("SELECT c.id, c.name, c.icon, c.color, c.group_id").
		WithArgs(testUserID()).
		WillReturnError(assert.AnError)

	req, _ := http.NewRequest(http.MethodGet, "/categories", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateCategory(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()

		oldPool := db.Pool
		db.Pool = mock
		defer func() { db.Pool = oldPool }()

		r := newCategoryTestRouter()
		userID := testUserID()

		catID := uuid.New()
		body := `{"name":"Rent","icon":"home","color":"#6366f1","groupId":"expense"}`
		expected := models.Category{ID: catID, Name: "Rent", Icon: "home", Color: "#6366f1", GroupID: "expense"}

		mock.ExpectQuery("INSERT INTO categories").
			WithArgs(userID, "Rent", "home", "#6366f1", "expense").
			WillReturnRows(pgxmock.NewRows([]string{"id", "name", "icon", "color", "group_id"}).
				AddRow(expected.ID, expected.Name, expected.Icon, expected.Color, expected.GroupID))

		req, _ := http.NewRequest(http.MethodPost, "/categories", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusCreated, w.Code)

		var cat models.Category
		err = json.Unmarshal(w.Body.Bytes(), &cat)
		assert.NoError(t, err)
		assert.Equal(t, catID, cat.ID)
		assert.Equal(t, "Rent", cat.Name)
		assert.Equal(t, "expense", cat.GroupID)

		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("invalid json", func(t *testing.T) {
		r := newCategoryTestRouter()

		req, _ := http.NewRequest(http.MethodPost, "/categories", bytes.NewBufferString("{"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("group not usable", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()

		oldPool := db.Pool
		db.Pool = mock
		defer func() { db.Pool = oldPool }()

		r := newCategoryTestRouter()

		mock.ExpectQuery("INSERT INTO categories").
			WithArgs(testUserID(), "Rent", "home", "#6366f1", "expense").
			WillReturnError(pgx.ErrNoRows)

		body := `{"name":"Rent","icon":"home","color":"#6366f1","groupId":"expense"}`
		req, _ := http.NewRequest(http.MethodPost, "/categories", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("database error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()

		oldPool := db.Pool
		db.Pool = mock
		defer func() { db.Pool = oldPool }()

		r := newCategoryTestRouter()

		mock.ExpectQuery("INSERT INTO categories").
			WithArgs(testUserID(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnError(assert.AnError)

		body := `{"name":"Rent","groupId":"expense"}`
		req, _ := http.NewRequest(http.MethodPost, "/categories", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestUpdateCategory(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()

		oldPool := db.Pool
		db.Pool = mock
		defer func() { db.Pool = oldPool }()

		r := newCategoryTestRouter()
		userID := testUserID()
		catID := uuid.New()

		mock.ExpectQuery("SELECT EXISTS").
			WithArgs("expense", userID).
			WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))

		mock.ExpectQuery("UPDATE categories").
			WithArgs(catID, "Groceries", "shopping-cart", "#84cc16", "expense", userID).
			WillReturnRows(pgxmock.NewRows([]string{"id", "name", "icon", "color", "group_id"}).
				AddRow(catID, "Groceries", "shopping-cart", "#84cc16", "expense"))

		body := `{"name":"Groceries","icon":"shopping-cart","color":"#84cc16","groupId":"expense"}`
		req, _ := http.NewRequest(http.MethodPut, "/categories/"+catID.String(), bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()

		oldPool := db.Pool
		db.Pool = mock
		defer func() { db.Pool = oldPool }()

		r := newCategoryTestRouter()
		userID := testUserID()
		catID := uuid.New()

		mock.ExpectQuery("SELECT EXISTS").
			WithArgs("expense", userID).
			WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))

		mock.ExpectQuery("UPDATE categories").
			WithArgs(catID, pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), "expense", userID).
			WillReturnError(pgx.ErrNoRows)

		body := `{"name":"Nope","groupId":"expense"}`
		req, _ := http.NewRequest(http.MethodPut, "/categories/"+catID.String(), bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestDeleteCategory(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	r := newCategoryTestRouter()
	userID := testUserID()
	catID := uuid.New()

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE transactions SET category_id = NULL").
		WithArgs(catID, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 3))
	mock.ExpectExec("DELETE FROM rules WHERE category_id").
		WithArgs(catID, userID).
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectExec("DELETE FROM categories WHERE id").
		WithArgs(catID, userID).
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectCommit()

	req, _ := http.NewRequest(http.MethodDelete, "/categories/"+catID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var result models.DeleteCategoryResult
	err = json.Unmarshal(w.Body.Bytes(), &result)
	assert.NoError(t, err)
	assert.Equal(t, 3, result.ClearedTransactions)
	assert.Equal(t, 1, result.DeletedRules)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteCategoryNotFound(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	r := newCategoryTestRouter()
	userID := testUserID()
	catID := uuid.New()

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE transactions SET category_id = NULL").
		WithArgs(catID, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	mock.ExpectExec("DELETE FROM rules WHERE category_id").
		WithArgs(catID, userID).
		WillReturnResult(pgxmock.NewResult("DELETE", 0))
	mock.ExpectExec("DELETE FROM categories WHERE id").
		WithArgs(catID, userID).
		WillReturnResult(pgxmock.NewResult("DELETE", 0))
	mock.ExpectRollback()

	req, _ := http.NewRequest(http.MethodDelete, "/categories/"+catID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}
