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
	"github.com/pashagolub/pgxmock/v3"
	"github.com/stretchr/testify/assert"
)

func newCategoryTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.GET("/categories", GetCategories)
	r.POST("/categories", CreateCategory)
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

	parentID := uuid.New()
	rows := pgxmock.NewRows([]string{"id", "name", "icon", "color", "parent_id", "type"}).
		AddRow(uuid.New(), "Food & Dining", "utensils", "#f97316", nil, "expense").
		AddRow(uuid.New(), "Salary", "wallet", "#22c55e", &parentID, "income")

	mock.ExpectQuery("SELECT id, name, icon, color, parent_id, type FROM categories WHERE user_id").
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
	assert.Nil(t, categories[0].ParentID)
	assert.Equal(t, "income", categories[1].Type)
	assert.NotNil(t, categories[1].ParentID)
	assert.Equal(t, parentID, *categories[1].ParentID)

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

	mock.ExpectQuery("SELECT id, name, icon, color, parent_id, type FROM categories").
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
		body := `{"name":"Rent","icon":"home","color":"#6366f1","type":"expense"}`
		expected := models.Category{ID: catID, Name: "Rent", Icon: "home", Color: "#6366f1", Type: "expense"}

		mock.ExpectQuery("INSERT INTO categories").
			WithArgs(userID, "Rent", "home", "#6366f1", pgxmock.AnyArg(), "expense").
			WillReturnRows(pgxmock.NewRows([]string{"id", "name", "icon", "color", "parent_id", "type"}).
				AddRow(expected.ID, expected.Name, expected.Icon, expected.Color, nil, expected.Type))

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
			WithArgs(testUserID(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnError(assert.AnError)

		body := `{"name":"Rent","type":"expense"}`
		req, _ := http.NewRequest(http.MethodPost, "/categories", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}
