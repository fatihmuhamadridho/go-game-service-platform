package controllers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	"example.com/learn-golang/internal/controller"
	"example.com/learn-golang/internal/model"
)

type AdminMemberHTTP struct {
	auth    *controller.AuthPresenter
	members *controller.AdminMemberController
}

func RegisterAdminMemberRoutes(mux *http.ServeMux, auth *controller.AuthPresenter, members *controller.AdminMemberController) {
	h := &AdminMemberHTTP{auth: auth, members: members}
	mux.HandleFunc("GET /api/admin/users/v1/list", h.list)
	mux.HandleFunc("POST /api/admin/users/v1/create", h.create)
	mux.HandleFunc("GET /api/admin/users/v1/detail/{userId}", h.detail)
	mux.HandleFunc("PATCH /api/admin/users/v1/update/{userId}", h.update)
	mux.HandleFunc("DELETE /api/admin/users/v1/delete/{userId}", h.delete)
}
func (h *AdminMemberHTTP) authorize(w http.ResponseWriter, r *http.Request) *controller.VerifiedToken {
	claims, err := h.auth.VerifyAccessToken(r.Context(), authToken(r, adminAccessCookie), model.AdminAccountType)
	if err != nil || claims == nil {
		writeJSON(w, 401, "USRB01001", "Sesi administrator tidak tersedia.", nil)
		return nil
	}
	if !controller.AdminPermissionAllowed(claims, "users-manage") {
		writeJSON(w, 403, "USRB01002", "Administrator tidak memiliki izin untuk tindakan ini.", nil)
		return nil
	}
	return claims
}
func (h *AdminMemberHTTP) fail(w http.ResponseWriter, err error) {
	e := controller.ToAdminMemberError(err)
	writeJSON(w, e.Status, e.Code, e.Message, nil)
}
func (h *AdminMemberHTTP) list(w http.ResponseWriter, r *http.Request) {
	if h.authorize(w, r) == nil {
		return
	}
	q := r.URL.Query()
	if !validMemberListQuery(q) {
		writeJSON(w, 400, "USRB02001", "Parameter pencarian atau pagination tidak valid.", nil)
		return
	}
	page, err := parseOptionalPositive(q, "page", 1, 0)
	if err != nil {
		writeJSON(w, 400, "USRB02001", "Parameter pencarian atau pagination tidak valid.", nil)
		return
	}
	pageSize, err := parseOptionalPositive(q, "pageSize", 20, 100)
	if err != nil {
		writeJSON(w, 400, "USRB02001", "Parameter pencarian atau pagination tidak valid.", nil)
		return
	}
	status := q.Get("status")
	search := q.Get("search")
	if (status != "" && status != "ACTIVE" && status != "BLOCKED") || controller.JSStringLength(search) > 100 {
		writeJSON(w, 400, "USRB02001", "Parameter pencarian atau pagination tidak valid.", nil)
		return
	}
	data, err := h.members.List(r.Context(), page, pageSize, status, strings.ToLower(search))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, 200, "USRS00000", "Daftar user berhasil diambil.", data)
}

func validMemberListQuery(q map[string][]string) bool {
	for key := range q {
		if key != "page" && key != "pageSize" && key != "search" && key != "status" {
			return false
		}
	}
	return true
}

func (h *AdminMemberHTTP) detail(w http.ResponseWriter, r *http.Request) {
	if h.authorize(w, r) == nil {
		return
	}
	data, err := h.members.Get(r.Context(), r.PathValue("userId"))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, 200, "USRS00000", "Detail user berhasil diambil.", map[string]any{"user": data})
}
func (h *AdminMemberHTTP) create(w http.ResponseWriter, r *http.Request) {
	if h.authorize(w, r) == nil {
		return
	}
	var body json.RawMessage
	if decodeBody(r, &body) != nil {
		writeJSON(w, 400, "USRB02001", "Data user tidak valid.", nil)
		return
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(body, &raw) != nil {
		raw = nil
	}
	in, ok := decodeMemberInputRaw(raw, true)
	if !ok {
		writeJSON(w, 400, "USRB02001", "Periksa nama, username, dan email member.", nil)
		return
	}
	if err := h.members.Create(r.Context(), in); err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, 201, "USRS00000", "Undangan berhasil dibuat.", nil)
}
func (h *AdminMemberHTTP) update(w http.ResponseWriter, r *http.Request) {
	claims := h.authorize(w, r)
	if claims == nil {
		return
	}
	in, ok := decodeMemberInput(r, false)
	if !ok {
		writeJSON(w, 400, "USRB02001", "Data user tidak valid.", nil)
		return
	}
	data, err := h.members.Update(r.Context(), r.PathValue("userId"), in, claims.Subject)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, 200, "USRS00000", "User berhasil diperbarui.", data)
}
func (h *AdminMemberHTTP) delete(w http.ResponseWriter, r *http.Request) {
	if h.authorize(w, r) == nil {
		return
	}
	data, err := h.members.Delete(r.Context(), r.PathValue("userId"))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, 200, "USRS00000", "User berhasil dihapus.", data)
}

func decodeMemberInput(r *http.Request, create bool) (controller.MemberInput, bool) {
	var raw map[string]json.RawMessage
	if decodeBody(r, &raw) != nil {
		return controller.MemberInput{}, false
	}
	return decodeMemberInputRaw(raw, create)
}

func decodeMemberInputRaw(raw map[string]json.RawMessage, create bool) (controller.MemberInput, bool) {
	if raw == nil {
		return controller.MemberInput{}, false
	}
	allowed := map[string]bool{"name": true, "username": true, "email": true, "whatsapp": !create, "status": !create, "accountType": create}
	for key := range raw {
		if !allowed[key] {
			return controller.MemberInput{}, false
		}
	}
	for _, key := range []string{"name", "username", "email", "status"} {
		if value, exists := raw[key]; exists && !isJSONString(value) {
			return controller.MemberInput{}, false
		}
	}
	if value, exists := raw["accountType"]; create && exists {
		accountType, ok := rawJSONString(value)
		if !ok || accountType != "MEMBER" {
			return controller.MemberInput{}, false
		}
	}
	var body struct {
		Name     *string `json:"name"`
		Username *string `json:"username"`
		Email    *string `json:"email"`
		Status   *string `json:"status"`
	}
	if json.Unmarshal(mustMarshal(raw), &body) != nil {
		return controller.MemberInput{}, false
	}
	var whatsapp model.OptionalString
	if value, exists := raw["whatsapp"]; exists {
		trim := bytes.TrimSpace(value)
		whatsapp.Set = true
		if !bytes.Equal(trim, []byte("null")) {
			var s string
			if json.Unmarshal(value, &s) != nil {
				return controller.MemberInput{}, false
			}
			whatsapp.Value = &s
		}
	}
	if body.Name != nil {
		*body.Name = controller.JSTrim(*body.Name)
	}
	return controller.MemberInput{Name: body.Name, Username: body.Username, Email: body.Email, WhatsApp: whatsapp, Status: body.Status}, true
}
func mustMarshal(value any) []byte { data, _ := json.Marshal(value); return data }
