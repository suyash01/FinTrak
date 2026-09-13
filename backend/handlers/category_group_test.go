package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pashagolub/pgxmock/v5"
	"github.com/stretchr/testify/assert"
)

func pgxErrUniqueViolation() error {
	return &pgconn.PgError{Code: "23505"}
}

func assertNoRows() error {
	return pgx.ErrNoRows
}

func newGroupTestRouter(srv *Server) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.GET("/groups", srv.GetGroups)
	r.POST("/groups", srv.CreateGroup)
	r.PUT("/groups/:id", srv.UpdateGroup)
	r.DELETE("/groups/:id", srv.DeleteGroup)
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
	srv := newTestServer(mock)

	r := newGroupTestRouter(srv)
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

func TestCreateGroupInvalidID(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)

	r := newGroupTestRouter(srv) // registers POST /groups -> CreateGroup

	// Rejected before any SQL: uppercase, delimiters that would make the id
	// unroutable via /groups/:id, digit-first, empty, and overlong (51 chars
	// > VARCHAR(50)).
	bodies := []string{
		`{"id":"BadID","name":"N"}`,
		`{"id":"bad/id","name":"N"}`,
		`{"id":"bad?x","name":"N"}`,
		`{"id":"1starts","name":"N"}`,
		`{"id":"","name":"N"}`,
		`{"id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","name":"N"}`,
	}
	for _, body := range bodies {
		req, _ := http.NewRequest(http.MethodPost, "/groups", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", body)
	}
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateGroup(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)

	r := newGroupTestRouter(srv)
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
	srv := newTestServer(mock)

	r := newGroupTestRouter(srv)

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
	srv := newTestServer(mock)

	r := newGroupTestRouter(srv)

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
	srv := newTestServer(mock)

	r := newGroupTestRouter(srv)
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
	srv := newTestServer(mock)

	r := newGroupTestRouter(srv)
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

	t.Run("create global group", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()
		srv := newTestServer(mock)

		r := gin.Default()
		r.Use(testAdminMiddleware())
		r.POST("/admin/groups", srv.CreateGlobalGroup)

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
		srv := newTestServer(mock)

		r := gin.Default()
		r.Use(testAdminMiddleware())
		r.POST("/admin/categories", srv.CreateGlobalCategory)

		catID := uuid.New()
		mock.ExpectQuery("INSERT INTO categories").
			WithArgs("Amazon Voucher", "gift", "#e11d48", "cashback").
			WillReturnRows(pgxmock.NewRows([]string{"id", "name", "icon", "color", "group_id"}).
				AddRow(catID, "Amazon Voucher", "gift", "#e11d48", "cashback"))

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

	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)

	r := gin.Default()
	r.Use(testAdminMiddleware())
	r.DELETE("/admin/categories/:id", srv.DeleteGlobalCategory)

	catID := uuid.New()

	mock.ExpectBegin()
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

func TestGetGroupsScanError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)

	r := newGroupTestRouter(srv)

	mock.ExpectQuery("SELECT id, name, icon, color, is_base, user_id, sort_order FROM category_groups").
		WithArgs(testUserID()).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("income"))

	req, _ := http.NewRequest(http.MethodGet, "/groups", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateGroupErrors(t *testing.T) {
	t.Run("immutable global group", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()
		r := newGroupTestRouter(newTestServer(mock))

		mock.ExpectQuery("UPDATE category_groups").
			WithArgs("expense", pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), testUserID()).
			WillReturnError(pgx.ErrNoRows)
		mock.ExpectQuery("SELECT EXISTS").
			WithArgs("expense").
			WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))

		req, _ := http.NewRequest(http.MethodPut, "/groups/expense", bytes.NewBufferString(`{"name":"Nope"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "immutable")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()
		r := newGroupTestRouter(newTestServer(mock))

		mock.ExpectQuery("UPDATE category_groups").
			WithArgs("ghost", pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), testUserID()).
			WillReturnError(pgx.ErrNoRows)
		mock.ExpectQuery("SELECT EXISTS").
			WithArgs("ghost").
			WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))

		req, _ := http.NewRequest(http.MethodPut, "/groups/ghost", bytes.NewBufferString(`{"name":"Nope"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found when existence check fails", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()
		r := newGroupTestRouter(newTestServer(mock))

		mock.ExpectQuery("UPDATE category_groups").
			WithArgs("ghost", pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), testUserID()).
			WillReturnError(pgx.ErrNoRows)
		mock.ExpectQuery("SELECT EXISTS").
			WithArgs("ghost").
			WillReturnError(assert.AnError)

		req, _ := http.NewRequest(http.MethodPut, "/groups/ghost", bytes.NewBufferString(`{"name":"Nope"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("database error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()
		r := newGroupTestRouter(newTestServer(mock))

		mock.ExpectQuery("UPDATE category_groups").
			WithArgs("vacation", pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), testUserID()).
			WillReturnError(assert.AnError)

		req, _ := http.NewRequest(http.MethodPut, "/groups/vacation", bytes.NewBufferString(`{"name":"Nope"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestDeleteGroupErrors(t *testing.T) {
	t.Run("count error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()
		r := newGroupTestRouter(newTestServer(mock))

		mock.ExpectQuery("SELECT COUNT").
			WithArgs("vacation", testUserID()).
			WillReturnError(assert.AnError)

		req, _ := http.NewRequest(http.MethodDelete, "/groups/vacation", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()
		r := newGroupTestRouter(newTestServer(mock))

		mock.ExpectQuery("SELECT COUNT").
			WithArgs("ghost", testUserID()).
			WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
		mock.ExpectExec("DELETE FROM category_groups").
			WithArgs("ghost", testUserID()).
			WillReturnResult(pgxmock.NewResult("DELETE", 0))

		req, _ := http.NewRequest(http.MethodDelete, "/groups/ghost", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("delete error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()
		r := newGroupTestRouter(newTestServer(mock))

		mock.ExpectQuery("SELECT COUNT").
			WithArgs("vacation", testUserID()).
			WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
		mock.ExpectExec("DELETE FROM category_groups").
			WithArgs("vacation", testUserID()).
			WillReturnError(assert.AnError)

		req, _ := http.NewRequest(http.MethodDelete, "/groups/vacation", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}
