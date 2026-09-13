package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v5"
	"github.com/stretchr/testify/assert"
)

// newAdminCatalogRouter registers the admin-only global category/group routes.
func newAdminCatalogRouter(srv *Server) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAdminMiddleware())
	r.POST("/admin/groups", srv.CreateGlobalGroup)
	r.POST("/admin/categories", srv.CreateGlobalCategory)
	r.PUT("/admin/categories/:id", srv.UpdateGlobalCategory)
	r.DELETE("/admin/categories/:id", srv.DeleteGlobalCategory)
	return r
}

func TestCreateGlobalGroupErrors(t *testing.T) {
	t.Run("invalid id", func(t *testing.T) {
		srv := newTestServer(nil)
		r := newAdminCatalogRouter(srv)

		body := `{"id":"Bad ID","name":"Nope"}`
		req, _ := http.NewRequest(http.MethodPost, "/admin/groups", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("conflict", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()
		r := newAdminCatalogRouter(newTestServer(mock))

		mock.ExpectQuery("INSERT INTO category_groups").
			WithArgs("dup", "Dup", "tag", "#ffffff").
			WillReturnError(pgxErrUniqueViolation())

		body := `{"id":"dup","name":"Dup","icon":"tag","color":"#ffffff"}`
		req, _ := http.NewRequest(http.MethodPost, "/admin/groups", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusConflict, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("database error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()
		r := newAdminCatalogRouter(newTestServer(mock))

		mock.ExpectQuery("INSERT INTO category_groups").
			WithArgs("oops", "Oops", "tag", "#ffffff").
			WillReturnError(assert.AnError)

		body := `{"id":"oops","name":"Oops","icon":"tag","color":"#ffffff"}`
		req, _ := http.NewRequest(http.MethodPost, "/admin/groups", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCreateGlobalCategoryErrors(t *testing.T) {
	t.Run("invalid json", func(t *testing.T) {
		r := newAdminCatalogRouter(newTestServer(nil))

		req, _ := http.NewRequest(http.MethodPost, "/admin/categories", bytes.NewBufferString("{"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("group not found", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()
		r := newAdminCatalogRouter(newTestServer(mock))

		mock.ExpectQuery("INSERT INTO categories").
			WithArgs("Voucher", "gift", "#e11d48", "missing").
			WillReturnError(pgx.ErrNoRows)

		body := `{"name":"Voucher","icon":"gift","color":"#e11d48","groupId":"missing"}`
		req, _ := http.NewRequest(http.MethodPost, "/admin/categories", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("conflict", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()
		r := newAdminCatalogRouter(newTestServer(mock))

		mock.ExpectQuery("INSERT INTO categories").
			WithArgs("Voucher", "gift", "#e11d48", "cashback").
			WillReturnError(pgxErrUniqueViolation())

		body := `{"name":"Voucher","icon":"gift","color":"#e11d48","groupId":"cashback"}`
		req, _ := http.NewRequest(http.MethodPost, "/admin/categories", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusConflict, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("database error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()
		r := newAdminCatalogRouter(newTestServer(mock))

		mock.ExpectQuery("INSERT INTO categories").
			WithArgs("Voucher", "gift", "#e11d48", "cashback").
			WillReturnError(assert.AnError)

		body := `{"name":"Voucher","icon":"gift","color":"#e11d48","groupId":"cashback"}`
		req, _ := http.NewRequest(http.MethodPost, "/admin/categories", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestUpdateGlobalCategory(t *testing.T) {
	t.Run("invalid id", func(t *testing.T) {
		r := newAdminCatalogRouter(newTestServer(nil))

		req, _ := http.NewRequest(http.MethodPut, "/admin/categories/not-a-uuid", bytes.NewBufferString(`{}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("invalid json", func(t *testing.T) {
		r := newAdminCatalogRouter(newTestServer(nil))

		req, _ := http.NewRequest(http.MethodPut, "/admin/categories/"+uuid.New().String(), bytes.NewBufferString("{"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("success without group change", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()
		r := newAdminCatalogRouter(newTestServer(mock))

		catID := uuid.New()
		mock.ExpectQuery("UPDATE categories").
			WithArgs(catID, "Renamed", "", "", "").
			WillReturnRows(pgxmock.NewRows([]string{"id", "name", "icon", "color", "group_id"}).
				AddRow(catID, "Renamed", "gift", "#e11d48", "cashback"))

		req, _ := http.NewRequest(http.MethodPut, "/admin/categories/"+catID.String(), bytes.NewBufferString(`{"name":"Renamed"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success with group change", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()
		r := newAdminCatalogRouter(newTestServer(mock))

		catID := uuid.New()
		mock.ExpectQuery("SELECT EXISTS").
			WithArgs("cashback").
			WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
		mock.ExpectQuery("UPDATE categories").
			WithArgs(catID, "", "", "", "cashback").
			WillReturnRows(pgxmock.NewRows([]string{"id", "name", "icon", "color", "group_id"}).
				AddRow(catID, "Voucher", "gift", "#e11d48", "cashback"))

		req, _ := http.NewRequest(http.MethodPut, "/admin/categories/"+catID.String(), bytes.NewBufferString(`{"groupId":"cashback"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("group check database error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()
		r := newAdminCatalogRouter(newTestServer(mock))

		mock.ExpectQuery("SELECT EXISTS").
			WithArgs("cashback").
			WillReturnError(assert.AnError)

		req, _ := http.NewRequest(http.MethodPut, "/admin/categories/"+uuid.New().String(), bytes.NewBufferString(`{"groupId":"cashback"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("referenced group not global", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()
		r := newAdminCatalogRouter(newTestServer(mock))

		mock.ExpectQuery("SELECT EXISTS").
			WithArgs("user_group").
			WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))

		req, _ := http.NewRequest(http.MethodPut, "/admin/categories/"+uuid.New().String(), bytes.NewBufferString(`{"groupId":"user_group"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()
		r := newAdminCatalogRouter(newTestServer(mock))

		mock.ExpectQuery("UPDATE categories").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnError(pgx.ErrNoRows)

		req, _ := http.NewRequest(http.MethodPut, "/admin/categories/"+uuid.New().String(), bytes.NewBufferString(`{"name":"Nope"}`))
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
		r := newAdminCatalogRouter(newTestServer(mock))

		mock.ExpectQuery("UPDATE categories").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnError(assert.AnError)

		req, _ := http.NewRequest(http.MethodPut, "/admin/categories/"+uuid.New().String(), bytes.NewBufferString(`{"name":"Nope"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestDeleteGlobalCategoryErrors(t *testing.T) {
	t.Run("invalid id", func(t *testing.T) {
		r := newAdminCatalogRouter(newTestServer(nil))

		req, _ := http.NewRequest(http.MethodDelete, "/admin/categories/not-a-uuid", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("not found", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()
		r := newAdminCatalogRouter(newTestServer(mock))

		catID := uuid.New()
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE transactions SET category_id = NULL").
			WithArgs(catID).
			WillReturnResult(pgxmock.NewResult("UPDATE", 0))
		mock.ExpectExec("DELETE FROM rules WHERE category_id").
			WithArgs(catID).
			WillReturnResult(pgxmock.NewResult("DELETE", 0))
		mock.ExpectExec("DELETE FROM categories WHERE id").
			WithArgs(catID).
			WillReturnResult(pgxmock.NewResult("DELETE", 0))
		mock.ExpectRollback()

		req, _ := http.NewRequest(http.MethodDelete, "/admin/categories/"+catID.String(), nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("transaction error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()
		r := newAdminCatalogRouter(newTestServer(mock))

		catID := uuid.New()
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE transactions SET category_id = NULL").
			WithArgs(catID).
			WillReturnError(assert.AnError)
		mock.ExpectRollback()

		req, _ := http.NewRequest(http.MethodDelete, "/admin/categories/"+catID.String(), nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}
