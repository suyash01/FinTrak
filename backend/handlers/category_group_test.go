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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pashagolub/pgxmock/v3"
	"github.com/stretchr/testify/assert"
)

func pgxErrUniqueViolation() error {
	return &pgconn.PgError{Code: "23505"}
}

func assertNoRows() error {
	return pgx.ErrNoRows
}

func newGroupTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.GET("/groups", GetGroups)
	r.POST("/groups", CreateGroup)
	r.PUT("/groups/:id", UpdateGroup)
	r.DELETE("/groups/:id", DeleteGroup)
	return r
}

func testAdminMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("userID", testUserID())
		c.Set("userRole", "admin")
		c.Next()
	}
}

func TestGetGroups(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	r := newGroupTestRouter()
	userID := testUserID()

	rows := pgxmock.NewRows([]string{"id", "name", "icon", "color", "is_base", "user_id", "sort_order"}).
		AddRow("income", "Income", "wallet", "#22c55e", true, nil, 1).
		AddRow("expense", "Expense", "shopping-bag", "#f97316", true, nil, 2).
		AddRow("vacation", "Vacation", "plane", "#0ea5e9", false, &userID, 5)

	mock.ExpectQuery("SELECT id, name, icon, color, is_base, user_id, sort_order FROM category_groups").
		WithArgs(userID).
		WillReturnRows(rows)

	req, _ := http.NewRequest(http.MethodGet, "/groups", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var groups []models.CategoryGroup
	err = json.Unmarshal(w.Body.Bytes(), &groups)
	assert.NoError(t, err)
	assert.Len(t, groups, 3)
	assert.True(t, groups[0].IsBase)
	assert.True(t, groups[0].IsGlobal)
	assert.False(t, groups[2].IsBase)
	assert.False(t, groups[2].IsGlobal)
	assert.Equal(t, userID, *groups[2].UserID)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateGroup(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	r := newGroupTestRouter()
	userID := testUserID()

	mock.ExpectQuery("INSERT INTO category_groups").
		WithArgs("vacation", "Vacation", "plane", "#0ea5e9", userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "icon", "color", "is_base", "user_id", "sort_order"}).
			AddRow("vacation", "Vacation", "plane", "#0ea5e9", false, &userID, 5))

	body := `{"id":"vacation","name":"Vacation","icon":"plane","color":"#0ea5e9"}`
	req, _ := http.NewRequest(http.MethodPost, "/groups", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateGroupConflict(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	r := newGroupTestRouter()

	mock.ExpectQuery("INSERT INTO category_groups").
		WithArgs("expense", pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), testUserID()).
		WillReturnError(pgxErrUniqueViolation())

	body := `{"id":"expense","name":"Expense"}`
	req, _ := http.NewRequest(http.MethodPost, "/groups", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateGroupImmutableBase(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	r := newGroupTestRouter()

	mock.ExpectQuery("UPDATE category_groups").
		WithArgs("income", pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), testUserID()).
		WillReturnError(assertNoRows())
	mock.ExpectQuery("SELECT EXISTS").
		WithArgs("income").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))

	body := `{"name":"Income"}`
	req, _ := http.NewRequest(http.MethodPut, "/groups/income", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteGroupBlocksWhenNotEmpty(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	r := newGroupTestRouter()
	userID := testUserID()

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM categories WHERE group_id").
		WithArgs("vacation", userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(2))

	req, _ := http.NewRequest(http.MethodDelete, "/groups/vacation", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteGroupSuccess(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	r := newGroupTestRouter()
	userID := testUserID()

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM categories WHERE group_id").
		WithArgs("vacation", userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec("DELETE FROM category_groups").
		WithArgs("vacation", userID).
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	req, _ := http.NewRequest(http.MethodDelete, "/groups/vacation", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateGlobalGroupAndCategory(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAdminMiddleware())
	r.POST("/admin/groups", CreateGlobalGroup)
	r.POST("/admin/categories", CreateGlobalCategory)

	t.Run("create global group", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()

		oldPool := db.Pool
		db.Pool = mock
		defer func() { db.Pool = oldPool }()

		mock.ExpectQuery("INSERT INTO category_groups").
			WithArgs("merchant_offers", "Merchant Offers", "tag", "#8b5cf6").
			WillReturnRows(pgxmock.NewRows([]string{"id", "name", "icon", "color", "is_base", "user_id", "sort_order"}).
				AddRow("merchant_offers", "Merchant Offers", "tag", "#8b5cf6", false, nil, 99))

		body := `{"id":"merchant_offers","name":"Merchant Offers","icon":"tag","color":"#8b5cf6"}`
		req, _ := http.NewRequest(http.MethodPost, "/admin/groups", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusCreated, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("create global category", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()

		oldPool := db.Pool
		db.Pool = mock
		defer func() { db.Pool = oldPool }()

		catID := uuid.New()
		mock.ExpectQuery("INSERT INTO categories").
			WithArgs("Amazon Voucher", "gift", "#e11d48", pgxmock.AnyArg(), "cashback").
			WillReturnRows(pgxmock.NewRows([]string{"id", "name", "icon", "color", "parent_id", "group_id"}).
				AddRow(catID, "Amazon Voucher", "gift", "#e11d48", nil, "cashback"))

		body := `{"name":"Amazon Voucher","icon":"gift","color":"#e11d48","groupId":"cashback"}`
		req, _ := http.NewRequest(http.MethodPost, "/admin/categories", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusCreated, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestDeleteGlobalCategory(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAdminMiddleware())
	r.DELETE("/admin/categories/:id", DeleteGlobalCategory)

	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	catID := uuid.New()

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE categories SET parent_id = NULL").
		WithArgs(catID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	mock.ExpectExec("UPDATE transactions SET category_id = NULL").
		WithArgs(catID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 5))
	mock.ExpectExec("DELETE FROM rules WHERE category_id").
		WithArgs(catID).
		WillReturnResult(pgxmock.NewResult("DELETE", 0))
	mock.ExpectExec("DELETE FROM categories WHERE id").
		WithArgs(catID).
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectCommit()

	req, _ := http.NewRequest(http.MethodDelete, "/admin/categories/"+catID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var result models.DeleteCategoryResult
	err = json.Unmarshal(w.Body.Bytes(), &result)
	assert.NoError(t, err)
	assert.Equal(t, 5, result.ClearedTransactions)

	assert.NoError(t, mock.ExpectationsWereMet())
}