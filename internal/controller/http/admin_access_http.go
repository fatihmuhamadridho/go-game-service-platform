package controllers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	"example.com/learn-golang/internal/controller"
	"example.com/learn-golang/internal/model"
)

type AdminAccessHTTP struct {
	auth   *controller.AuthPresenter
	access *controller.AdminAccessController
}

func RegisterAdminAccessRoutes(mux *http.ServeMux, auth *controller.AuthPresenter, access *controller.AdminAccessController) {
	h := &AdminAccessHTTP{auth: auth, access: access}
	mux.HandleFunc("GET /api/admin/roles/v1/list", h.listRoles)
	mux.HandleFunc("POST /api/admin/roles/v1/create", h.createRole)
	mux.HandleFunc("GET /api/admin/roles/v1/detail/{roleId}", h.getRole)
	mux.HandleFunc("PUT /api/admin/roles/v1/update/{roleId}", h.updateRole)
	mux.HandleFunc("DELETE /api/admin/roles/v1/delete/{roleId}", h.deleteRole)
	mux.HandleFunc("GET /api/admin/admin-users/v1/list", h.listAccounts)
	mux.HandleFunc("POST /api/admin/admin-users/v1/create", h.createAccount)
	mux.HandleFunc("GET /api/admin/admin-users/v1/detail/{adminId}", h.getAccount)
	mux.HandleFunc("PATCH /api/admin/admin-users/v1/update/{adminId}", h.updateAccount)
	mux.HandleFunc("DELETE /api/admin/admin-users/v1/delete/{adminId}", h.deleteAccount)
}
func (h *AdminAccessHTTP) authorize(w http.ResponseWriter, r *http.Request, code, permission string) *controller.VerifiedToken {
	claims, e := h.auth.VerifyAccessToken(r.Context(), authToken(r, adminAccessCookie), model.AdminAccountType)
	if e != nil || claims == nil {
		writeJSON(w, 401, code, "Sesi administrator tidak tersedia.", nil)
		return nil
	}
	if permission != "" && !controller.AdminPermissionAllowed(claims, permission) {
		writeJSON(w, 403, strings.Replace(code, "01001", "01002", 1), "Administrator tidak memiliki izin untuk tindakan ini.", nil)
		return nil
	}
	return claims
}
func (h *AdminAccessHTTP) fail(w http.ResponseWriter, e error, prefix string) {
	x := controller.ToAdminAccessError(e)
	if prefix == "ROL" && !strings.HasPrefix(x.Code, "ROL") {
		x.Code, x.Message = "ROLT03001", "Data role sementara tidak tersedia."
	}
	if prefix == "USR" && strings.HasPrefix(x.Code, "ROL") {
		x.Code, x.Message = "USRT03001", "Data akun admin sementara tidak tersedia."
	}
	writeJSON(w, x.Status, x.Code, x.Message, nil)
}
func (h *AdminAccessHTTP) listRoles(w http.ResponseWriter, r *http.Request) {
	if h.authorize(w, r, "ROLB01001", "settings-manage") == nil {
		return
	}
	data, e := h.access.ListRoles(r.Context())
	if e != nil {
		h.fail(w, e, "ROL")
		return
	}
	writeJSON(w, 200, "ROLS00000", "Daftar role berhasil diambil.", data)
}
func (h *AdminAccessHTTP) getRole(w http.ResponseWriter, r *http.Request) {
	if h.authorize(w, r, "ROLB01001", "settings-manage") == nil {
		return
	}
	data, e := h.access.GetRole(r.Context(), r.PathValue("roleId"))
	if e != nil {
		h.fail(w, e, "ROL")
		return
	}
	writeJSON(w, 200, "ROLS00000", "Detail role berhasil diambil.", map[string]any{"role": data})
}
func (h *AdminAccessHTTP) createRole(w http.ResponseWriter, r *http.Request) {
	claims := h.authorize(w, r, "ROLB01001", "settings-manage")
	if claims == nil {
		return
	}
	in, ok := decodeRoleInput(r, false)
	if !ok {
		writeJSON(w, 400, "ROLB02001", "Data role tidak valid.", nil)
		return
	}
	data, e := h.access.CreateRole(r.Context(), in, requestActivityContext(r, claims))
	if e != nil {
		h.fail(w, e, "ROL")
		return
	}
	writeJSON(w, 201, "ROLS00000", "Role berhasil dibuat.", map[string]any{"role": data})
}
func (h *AdminAccessHTTP) updateRole(w http.ResponseWriter, r *http.Request) {
	claims := h.authorize(w, r, "ROLB01001", "settings-manage")
	if claims == nil {
		return
	}
	in, ok := decodeRoleInput(r, true)
	if !ok {
		writeJSON(w, 400, "ROLB02001", "Data role tidak valid.", nil)
		return
	}
	data, e := h.access.UpdateRole(r.Context(), r.PathValue("roleId"), in, requestActivityContext(r, claims))
	if e != nil {
		h.fail(w, e, "ROL")
		return
	}
	writeJSON(w, 200, "ROLS00000", "Role berhasil diperbarui.", map[string]any{"role": data})
}
func (h *AdminAccessHTTP) deleteRole(w http.ResponseWriter, r *http.Request) {
	claims := h.authorize(w, r, "ROLB01001", "settings-manage")
	if claims == nil {
		return
	}
	data, e := h.access.DeleteRole(r.Context(), r.PathValue("roleId"), requestActivityContext(r, claims))
	if e != nil {
		h.fail(w, e, "ROL")
		return
	}
	writeJSON(w, 200, "ROLS00000", "Role berhasil dihapus.", data)
}
func (h *AdminAccessHTTP) listAccounts(w http.ResponseWriter, r *http.Request) {
	if h.authorize(w, r, "USRB01001", "settings-manage") == nil {
		return
	}
	q := r.URL.Query()
	page, e := parseOptionalPositive(q, "page", 1, 0)
	if e != nil {
		writeJSON(w, 400, "USRB02001", "Filter admin tidak valid.", nil)
		return
	}
	size, e := parseOptionalPositive(q, "pageSize", 20, 100)
	if e != nil {
		writeJSON(w, 400, "USRB02001", "Filter admin tidak valid.", nil)
		return
	}
	status := q.Get("status")
	if status != "" && status != "ACTIVE" && status != "INACTIVE" || controller.JSStringLength(q.Get("search")) > 100 {
		writeJSON(w, 400, "USRB02001", "Filter admin tidak valid.", nil)
		return
	}
	for key := range q {
		if key != "page" && key != "pageSize" && key != "search" && key != "status" && key != "roleId" {
			writeJSON(w, 400, "USRB02001", "Filter admin tidak valid.", nil)
			return
		}
	}
	data, e := h.access.ListAccounts(r.Context(), q.Get("search"), status, q.Get("roleId"), page, size)
	if e != nil {
		h.fail(w, e, "USR")
		return
	}
	writeJSON(w, 200, "USRS00000", "Daftar admin berhasil diambil.", data)
}
func (h *AdminAccessHTTP) getAccount(w http.ResponseWriter, r *http.Request) {
	if h.authorize(w, r, "USRB01001", "settings-manage") == nil {
		return
	}
	data, e := h.access.GetAccount(r.Context(), r.PathValue("adminId"))
	if e != nil {
		h.fail(w, e, "USR")
		return
	}
	writeJSON(w, 200, "USRS00000", "Detail admin berhasil diambil.", map[string]any{"admin": data})
}
func (h *AdminAccessHTTP) createAccount(w http.ResponseWriter, r *http.Request) {
	claims := h.authorize(w, r, "USRB01001", "settings-manage")
	if claims == nil {
		return
	}
	if claims.Role != "SUPER_ADMIN" {
		writeJSON(w, 403, "USRB01002", "Hanya Super Admin dapat membuat akun admin.", nil)
		return
	}
	var body json.RawMessage
	if decodeBody(r, &body) != nil {
		writeJSON(w, 400, "USRB02001", "Data admin tidak valid.", nil)
		return
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(body, &raw) != nil {
		raw = nil
	}
	in, ok, _ := decodeAdminAccountInputRaw(raw, true)
	if !ok {
		writeJSON(w, 400, "USRB02001", "Nama, username, email, dan role wajib valid.", nil)
		return
	}
	if e := h.access.CreateAccount(r.Context(), in); e != nil {
		h.fail(w, e, "USR")
		return
	}
	writeJSON(w, 503, "USRT02001", "Undangan akun admin belum tersedia karena identity provider belum dikonfigurasi.", nil)
}
func (h *AdminAccessHTTP) updateAccount(w http.ResponseWriter, r *http.Request) {
	claims := h.authorize(w, r, "USRB01001", "settings-manage")
	if claims == nil {
		return
	}
	in, ok, hasRoleIDs := decodeAdminAccountInput(r, false)
	if hasRoleIDs && claims.Role != "SUPER_ADMIN" {
		writeJSON(w, 403, "USRB01002", "Hanya Super Admin dapat mengubah assignment role.", nil)
		return
	}
	if !ok {
		writeJSON(w, 400, "USRB02001", "Data admin tidak valid.", nil)
		return
	}
	data, e := h.access.UpdateAccount(r.Context(), r.PathValue("adminId"), in, claims.Subject, claims.Role == "SUPER_ADMIN")
	if e != nil {
		h.fail(w, e, "USR")
		return
	}
	writeJSON(w, 200, "USRS00000", "Admin berhasil diperbarui.", map[string]any{"admin": data})
}
func (h *AdminAccessHTTP) deleteAccount(w http.ResponseWriter, r *http.Request) {
	claims := h.authorize(w, r, "USRB01001", "settings-manage")
	if claims == nil {
		return
	}
	data, e := h.access.DeleteAccount(r.Context(), r.PathValue("adminId"), claims.Subject)
	if e != nil {
		h.fail(w, e, "USR")
		return
	}
	writeJSON(w, 200, "USRS00000", "Admin berhasil dinonaktifkan.", data)
}
func decodeRoleInput(r *http.Request, partial bool) (controller.RoleInput, bool) {
	var raw map[string]json.RawMessage
	if decodeBody(r, &raw) != nil {
		return controller.RoleInput{}, false
	}
	allowed := map[string]bool{"name": true, "code": true, "description": true, "status": true, "permissions": true}
	for k := range raw {
		if !allowed[k] {
			return controller.RoleInput{}, false
		}
	}
	var in controller.RoleInput
	if v, ok := raw["name"]; ok {
		if !isJSONString(v) || json.Unmarshal(v, &in.Name) != nil {
			return in, false
		}
	}
	if v, ok := raw["code"]; ok {
		if !isJSONString(v) || json.Unmarshal(v, &in.Code) != nil {
			return in, false
		}
	}
	if v, ok := raw["status"]; ok {
		if !isJSONString(v) || json.Unmarshal(v, &in.Status) != nil {
			return in, false
		}
	}
	if v, ok := raw["permissions"]; ok {
		if len(bytes.TrimSpace(v)) == 0 || bytes.TrimSpace(v)[0] != '[' {
			return in, false
		}
		var ids []string
		if json.Unmarshal(v, &ids) != nil {
			return in, false
		}
		in.Permissions = &ids
	}
	if v, ok := raw["description"]; ok {
		in.Description.Set = true
		if !bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			var s string
			if !isJSONString(v) || json.Unmarshal(v, &s) != nil {
				return in, false
			}
			in.Description.Value = &s
		}
	}
	if partial && len(raw) == 0 {
		return in, false
	}
	return in, validRoleInputHTTP(in, partial)
}
func validRoleInputHTTP(in controller.RoleInput, partial bool) bool {
	if partial && in.Name == nil && in.Code == nil && !in.Description.Set && in.Status == nil && in.Permissions == nil {
		return false
	}
	if !partial && (in.Name == nil || in.Code == nil) {
		return false
	}
	return true
}
func decodeAdminAccountInput(r *http.Request, create bool) (controller.AdminAccountInput, bool, bool) {
	var raw map[string]json.RawMessage
	if decodeBody(r, &raw) != nil {
		return controller.AdminAccountInput{}, false, false
	}
	return decodeAdminAccountInputRaw(raw, create)
}

func decodeAdminAccountInputRaw(raw map[string]json.RawMessage, create bool) (controller.AdminAccountInput, bool, bool) {
	if raw == nil {
		return controller.AdminAccountInput{}, false, false
	}
	_, hasRoleIDs := raw["roleIds"]
	allowed := map[string]bool{"name": true, "username": true, "email": true, "roleIds": true, "status": !create}
	for k := range raw {
		if !allowed[k] {
			return controller.AdminAccountInput{}, false, hasRoleIDs
		}
	}
	var in controller.AdminAccountInput
	if v, ok := raw["name"]; ok {
		if !isJSONString(v) || json.Unmarshal(v, &in.Name) != nil {
			return in, false, hasRoleIDs
		}
	}
	if v, ok := raw["username"]; ok {
		if !isJSONString(v) || json.Unmarshal(v, &in.Username) != nil {
			return in, false, hasRoleIDs
		}
	}
	if v, ok := raw["email"]; ok {
		if !isJSONString(v) || json.Unmarshal(v, &in.Email) != nil {
			return in, false, hasRoleIDs
		}
	}
	if v, ok := raw["status"]; ok {
		if !isJSONString(v) || json.Unmarshal(v, &in.Status) != nil {
			return in, false, hasRoleIDs
		}
	}
	if v, ok := raw["roleIds"]; ok {
		if len(bytes.TrimSpace(v)) == 0 || bytes.TrimSpace(v)[0] != '[' {
			return in, false, hasRoleIDs
		}
		var ids []string
		if json.Unmarshal(v, &ids) != nil {
			return in, false, hasRoleIDs
		}
		in.RoleIDs = &ids
	}
	if create && (in.Name == nil || in.Username == nil || in.Email == nil || in.RoleIDs == nil || len(*in.RoleIDs) == 0) {
		return in, false, hasRoleIDs
	}
	if !create && len(raw) == 0 {
		return in, false, hasRoleIDs
	}
	return in, true, hasRoleIDs
}
func isJSONString(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '"'
}
