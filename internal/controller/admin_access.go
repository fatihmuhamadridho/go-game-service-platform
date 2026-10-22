package controller

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"strings"
	"time"

	"example.com/learn-golang/internal/model"
	"go.mongodb.org/mongo-driver/v2/bson"
	"golang.org/x/text/cases"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

type AdminAccessError struct {
	Code, Message string
	Status        int
}

func (e *AdminAccessError) Error() string { return e.Message }
func ToAdminAccessError(err error) *AdminAccessError {
	var e *AdminAccessError
	if errors.As(err, &e) {
		return e
	}
	return &AdminAccessError{Code: "ROLT03001", Message: "Data role sementara tidak tersedia.", Status: 500}
}
func accessFailure(code, message string, status int) error {
	return &AdminAccessError{Code: code, Message: message, Status: status}
}

type AdminAccessController struct {
	roles    model.AdminRoleRepository
	accounts model.AdminAccountRepository
}

func NewAdminAccessController(roles model.AdminRoleRepository, accounts model.AdminAccountRepository) *AdminAccessController {
	return &AdminAccessController{roles: roles, accounts: accounts}
}

var permissionDefs = []model.Permission{
	{Code: "dashboard-read", Name: "Dashboard", Description: "Izin dashboard pada dashboard."},
	{Code: "orders-manage", Name: "Pesanan", Description: "Izin pesanan pada dashboard."},
	{Code: "catalog-manage", Name: "Produk & Layanan", Description: "Izin produk & layanan pada dashboard."},
	{Code: "users-manage", Name: "Pengguna", Description: "Izin pengguna pada dashboard.", IsSensitive: true},
	{Code: "content-manage", Name: "Konten", Description: "Izin konten pada dashboard."},
	{Code: "finance-read", Name: "Keuangan", Description: "Izin keuangan pada dashboard."},
	{Code: "settings-manage", Name: "Pengaturan", Description: "Izin pengaturan pada dashboard.", IsSensitive: true},
}
var roleCodePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,49}$`)

type RoleInput struct {
	Name        *string `json:"name"`
	Code        *string `json:"code"`
	Description model.OptionalString
	Status      *string   `json:"status"`
	Permissions *[]string `json:"permissions"`
}
type AdminAccountInput struct {
	Name     *string
	Username *string
	Email    *string
	Status   *string
	RoleIDs  *[]string
}

func (c *AdminAccessController) ListRoles(ctx context.Context) (map[string]any, error) {
	roles, e := c.roles.ListRoles(ctx)
	if e != nil {
		return nil, e
	}
	roles = filterRoles(roles)
	perms, e := c.roles.EnsurePermissions(ctx, permissionDefs)
	if e != nil {
		return nil, e
	}
	links, e := c.roles.ListRolePermissions(ctx)
	if e != nil {
		return nil, e
	}
	allPerms, e := c.roles.ListPermissions(ctx)
	if e != nil {
		return nil, e
	}
	accountLinks, e := c.roles.ListAccountRoles(ctx)
	if e != nil {
		return nil, e
	}
	out := make([]map[string]any, 0, len(roles))
	for _, r := range roles {
		out = append(out, roleDTO(r, links, allPerms, accountLinks))
	}
	catalog := make([]map[string]any, 0, len(perms))
	for _, p := range perms {
		catalog = append(catalog, map[string]any{"id": p.ID.Hex(), "code": p.Code, "name": p.Name, "key": permissionKey(p.Code)})
	}
	return map[string]any{"roles": out, "permissionCatalog": catalog}, nil
}
func (c *AdminAccessController) GetRole(ctx context.Context, raw string) (map[string]any, error) {
	id, e := bson.ObjectIDFromHex(raw)
	if e != nil {
		return nil, accessFailure("ROLB02001", "ID role tidak valid.", 400)
	}
	r, e := c.roles.FindRole(ctx, id)
	if e != nil {
		return nil, e
	}
	if r == nil || r.DeletedAt != nil {
		return nil, accessFailure("ROLB02002", "Role tidak ditemukan.", 404)
	}
	links, e := c.roles.ListRolePermissions(ctx)
	if e != nil {
		return nil, e
	}
	perms, e := c.roles.ListPermissions(ctx)
	if e != nil {
		return nil, e
	}
	accounts, e := c.roles.ListAccountRoles(ctx)
	if e != nil {
		return nil, e
	}
	return roleDTO(*r, links, perms, accounts), nil
}
func (c *AdminAccessController) CreateRole(ctx context.Context, in RoleInput, audit model.ActivityContext) (map[string]any, error) {
	if !validRoleInput(in, false) {
		return nil, accessFailure("ROLB02001", "Data role tidak valid.", 400)
	}
	name := JSTrim(*in.Name)
	code := *in.Code
	if e := c.checkRoleUnique(ctx, name, code, bson.NilObjectID); e != nil {
		return nil, e
	}
	now := time.Now().UTC()
	role := model.AdminRole{ID: bson.NewObjectID(), Name: name, Code: code, Status: roleStatus(in.Status), Description: normalizedString(in.Description), CreatedAt: now, UpdatedAt: now}
	if e := c.roles.CreateRole(ctx, role); e != nil {
		return nil, e
	}
	if in.Permissions != nil {
		selected, e := c.selectPermissions(ctx, *in.Permissions)
		if e != nil {
			return nil, e
		}
		if e = c.roles.ReplaceRolePermissions(ctx, role.ID, selected, audit.ActorID, now); e != nil {
			return nil, e
		}
	}
	changed := map[string]any{"name": *in.Name, "code": code}
	if in.Description.Set {
		changed["description"] = in.Description.Value
	}
	if in.Status != nil {
		changed["status"] = *in.Status
	}
	if in.Permissions != nil {
		changed["permissions"] = *in.Permissions
	}
	_ = c.roles.RecordAdminRoleActivity(ctx, audit, "ADMIN_ROLE_CREATED", role.ID, changed)
	return c.GetRole(ctx, role.ID.Hex())
}
func (c *AdminAccessController) UpdateRole(ctx context.Context, raw string, in RoleInput, audit model.ActivityContext) (map[string]any, error) {
	id, e := bson.ObjectIDFromHex(raw)
	if e != nil {
		return nil, accessFailure("ROLB02001", "ID role tidak valid.", 400)
	}
	role, e := c.roles.FindRole(ctx, id)
	if e != nil {
		return nil, e
	}
	if role == nil || role.DeletedAt != nil {
		return nil, accessFailure("ROLB02002", "Role tidak ditemukan.", 404)
	}
	if role.IsSystem {
		return nil, accessFailure("ROLB02004", "Role system tidak dapat diubah.", 409)
	}
	if !validRoleInput(in, true) {
		return nil, accessFailure("ROLB02001", "Data role tidak valid.", 400)
	}
	name, code := role.Name, role.Code
	if in.Name != nil {
		name = JSTrim(*in.Name)
	}
	if in.Code != nil {
		code = *in.Code
	}
	if in.Name != nil || in.Code != nil {
		if e = c.checkRoleUnique(ctx, name, code, id); e != nil {
			return nil, e
		}
	}
	patch := map[string]any{}
	if in.Name != nil {
		patch["name"] = name
	}
	if in.Code != nil {
		patch["code"] = code
	}
	if in.Description.Set {
		patch["description"] = normalizedString(in.Description)
	}
	if in.Status != nil {
		patch["status"] = *in.Status
	}
	updatedAt := time.Now().UTC()
	if len(patch) > 0 || in.Permissions != nil {
		patch["updated_at"] = updatedAt
		if e = c.roles.UpdateRole(ctx, id, patch); e != nil {
			return nil, e
		}
	}
	if in.Permissions != nil {
		selected, e := c.selectPermissions(ctx, *in.Permissions)
		if e != nil {
			return nil, e
		}
		if e = c.roles.ReplaceRolePermissions(ctx, id, selected, audit.ActorID, time.Now().UTC()); e != nil {
			return nil, e
		}
	}
	changed := make(map[string]any, len(patch)+1)
	for key, value := range patch {
		if key == "updated_at" {
			value = updatedAt.Format(time.RFC3339Nano)
		}
		changed[key] = value
	}
	if in.Permissions != nil {
		changed["permissions"] = *in.Permissions
	}
	_ = c.roles.RecordAdminRoleActivity(ctx, audit, "ADMIN_ROLE_UPDATED", id, changed)
	return c.GetRole(ctx, raw)
}
func (c *AdminAccessController) DeleteRole(ctx context.Context, raw string, audit model.ActivityContext) (map[string]any, error) {
	id, e := bson.ObjectIDFromHex(raw)
	if e != nil {
		return nil, accessFailure("ROLB02001", "ID role tidak valid.", 400)
	}
	r, e := c.roles.FindRole(ctx, id)
	if e != nil {
		return nil, e
	}
	if r == nil || r.DeletedAt != nil {
		return nil, accessFailure("ROLB02002", "Role tidak ditemukan.", 404)
	}
	if r.IsSystem {
		return nil, accessFailure("ROLB02004", "Role system tidak dapat dihapus.", 409)
	}
	count, e := c.roles.CountAssignedAccountsForRole(ctx, id)
	if e != nil {
		return nil, e
	}
	if count > 0 {
		return nil, accessFailure("ROLB02005", "Role masih digunakan akun admin.", 409)
	}
	now := time.Now().UTC()
	if e = c.roles.SoftDeleteRole(ctx, id, now); e != nil {
		return nil, e
	}
	_ = c.roles.RecordAdminRoleActivity(ctx, audit, "ADMIN_ROLE_DELETED", id, map[string]any{"deleted_at": now.Format(time.RFC3339Nano)})
	return map[string]any{"id": id.Hex(), "deletedAt": now}, nil
}

func (c *AdminAccessController) ListAccounts(ctx context.Context, search, status, role string, page, size int) (map[string]any, error) {
	accounts, e := c.accounts.ListAdminAccounts(ctx)
	if e != nil {
		return nil, e
	}
	links, e := c.accounts.ListRoleLinks(ctx)
	if e != nil {
		return nil, e
	}
	roles, e := c.roles.ListRoles(ctx)
	if e != nil {
		return nil, e
	}
	search = cases.Lower(language.Indonesian).String(search)
	rows := make([]map[string]any, 0)
	for _, a := range accounts {
		if a.DeletedAt != nil || (status != "" && a.Status != status) || (search != "" && !strings.Contains(cases.Lower(language.Indonesian).String(a.Name+" "+a.Username+" "+a.Email), search)) {
			continue
		}
		ids, names := []string{}, []string{}
		super := false
		for _, roleRow := range roles {
			if roleRow.DeletedAt != nil || roleRow.Status != "ACTIVE" {
				continue
			}
			assigned := false
			for _, link := range links {
				if link.AdminAccountID == a.ID && link.RoleID == roleRow.ID && link.RevokedAt == nil {
					assigned = true
					break
				}
			}
			if assigned {
				ids = append(ids, roleRow.ID.Hex())
				names = append(names, roleRow.Name)
				super = super || roleRow.IsSystem
			}
		}
		if role != "" && !containsString(ids, role) {
			continue
		}
		roleName := "Tanpa Role"
		if len(names) > 0 {
			roleName = "Admin"
		}
		if super {
			roleName = "Super Admin"
		}
		roleID := firstString(ids)
		for _, roleRow := range roles {
			if roleRow.IsSystem && containsString(ids, roleRow.ID.Hex()) {
				roleID = roleRow.ID.Hex()
				break
			}
		}
		rows = append(rows, map[string]any{"id": a.ID.Hex(), "username": a.Username, "name": a.Name, "email": a.Email, "status": a.Status, "roleId": roleID, "role": roleName, "roleIds": ids, "roleNames": names, "lastActive": "Belum tersedia", "updatedAt": a.UpdatedAt})
	}
	nameCollator := collate.New(language.Indonesian)
	sort.SliceStable(rows, func(i, j int) bool {
		return nameCollator.CompareString(rows[i]["name"].(string), rows[j]["name"].(string)) < 0
	})
	total := len(rows)
	start := (page - 1) * size
	if start > total {
		start = total
	}
	end := start + size
	if end > total {
		end = total
	}
	return map[string]any{"admins": rows[start:end], "total": total, "pagination": map[string]int{"page": page, "pageSize": size, "total": total, "totalPages": (total + size - 1) / size}}, nil
}
func (c *AdminAccessController) GetAccount(ctx context.Context, raw string) (map[string]any, error) {
	id, e := bson.ObjectIDFromHex(raw)
	if e != nil {
		return nil, accessFailure("USRB02001", "ID admin tidak valid.", 400)
	}
	a, e := c.accounts.FindAdminAccount(ctx, id)
	if e != nil {
		return nil, e
	}
	if a == nil || a.DeletedAt != nil {
		return nil, accessFailure("USRB02004", "Admin tidak ditemukan.", 404)
	}
	links, e := c.accounts.ListRoleLinks(ctx)
	if e != nil {
		return nil, e
	}
	roles, e := c.roles.ListRoles(ctx)
	if e != nil {
		return nil, e
	}
	ids := []string{}
	assigned := []map[string]any{}
	for _, roleRow := range roles {
		if roleRow.Status != "ACTIVE" || roleRow.DeletedAt != nil {
			continue
		}
		for _, link := range links {
			if link.AdminAccountID == id && link.RoleID == roleRow.ID && link.RevokedAt == nil {
				ids = append(ids, roleRow.ID.Hex())
				assigned = append(assigned, map[string]any{"id": roleRow.ID.Hex(), "name": roleRow.Name, "code": roleRow.Code, "system": roleRow.IsSystem})
				break
			}
		}
	}
	return map[string]any{"id": id.Hex(), "username": a.Username, "name": a.Name, "email": a.Email, "status": a.Status, "roleIds": ids, "roles": assigned, "createdAt": a.CreatedAt, "updatedAt": a.UpdatedAt}, nil
}
func (c *AdminAccessController) CreateAccount(ctx context.Context, in AdminAccountInput) error {
	if !validAdminAccount(in, true) {
		return accessFailure("USRB02001", "Nama, username, email, dan role wajib valid.", 400)
	}
	u := strings.ToLower(JSTrim(*in.Username))
	mail := strings.ToLower(JSTrim(*in.Email))
	old, e := c.accounts.FindAdminAccountByUsernameOrEmail(ctx, u, mail)
	if e != nil {
		return e
	}
	if old != nil {
		return accessFailure("USRB02002", "Username atau email admin sudah digunakan.", 409)
	}
	if _, e = c.validAccountRoleIDs(ctx, *in.RoleIDs); e != nil {
		return e
	}
	return accessFailure("USRT02001", "Undangan akun admin belum tersedia karena identity provider belum dikonfigurasi.", 503)
}
func (c *AdminAccessController) UpdateAccount(ctx context.Context, raw string, in AdminAccountInput, actor bson.ObjectID, allowRoleChange bool) (map[string]any, error) {
	id, e := bson.ObjectIDFromHex(raw)
	if e != nil {
		return nil, accessFailure("USRB02001", "ID admin tidak valid.", 400)
	}
	if !validAdminAccount(in, false) {
		return nil, accessFailure("USRB02001", "Data admin tidak valid.", 400)
	}
	a, e := c.accounts.FindAdminAccount(ctx, id)
	if e != nil {
		return nil, e
	}
	if a == nil || a.DeletedAt != nil {
		return nil, accessFailure("USRB02004", "Admin tidak ditemukan.", 404)
	}
	if in.RoleIDs != nil && !allowRoleChange {
		return nil, accessFailure("USRB01002", "Hanya Super Admin dapat mengubah assignment role.", 403)
	}
	username, email := a.Username, a.Email
	if in.Username != nil {
		username = strings.ToLower(JSTrim(*in.Username))
	}
	if in.Email != nil {
		email = strings.ToLower(JSTrim(*in.Email))
	}
	if username != a.Username || email != a.Email {
		usernameToCheck, emailToCheck := "", ""
		if username != a.Username {
			usernameToCheck = username
		}
		if email != a.Email {
			emailToCheck = email
		}
		existing, e := c.accounts.FindAdminAccountByUsernameOrEmail(ctx, usernameToCheck, emailToCheck, id)
		if e != nil {
			return nil, e
		}
		if existing != nil && existing.ID != id {
			return nil, accessFailure("USRB02002", "Username atau email admin sudah digunakan.", 409)
		}
	}
	links, e := c.accounts.ListRoleLinks(ctx)
	if e != nil {
		return nil, e
	}
	roles, e := c.roles.ListRoles(ctx)
	if e != nil {
		return nil, e
	}
	oldSuper := false
	for _, l := range links {
		if l.AdminAccountID == id && l.RevokedAt == nil {
			for _, r := range roles {
				oldSuper = oldSuper || (r.ID == l.RoleID && r.DeletedAt == nil && r.IsSystem && r.Code == "SUPER_ADMIN")
			}
		}
	}
	newIDs := []bson.ObjectID{}
	if in.RoleIDs != nil {
		newIDs, e = c.validAccountRoleIDs(ctx, *in.RoleIDs)
		if e != nil {
			return nil, e
		}
	} else {
		for _, l := range links {
			if l.AdminAccountID == id && l.RevokedAt == nil {
				newIDs = append(newIDs, l.RoleID)
			}
		}
	}
	newSuper := false
	for _, rid := range newIDs {
		for _, r := range roles {
			newSuper = newSuper || (r.ID == rid && r.IsSystem && r.Code == "SUPER_ADMIN")
		}
	}
	if oldSuper && (!newSuper || (in.Status != nil && *in.Status == "INACTIVE")) {
		count, e := c.countActiveSupers(ctx, roles, links)
		if e != nil {
			return nil, e
		}
		if count <= 1 {
			return nil, accessFailure("USRB02005", "Super Admin aktif terakhir tidak dapat dinonaktifkan atau kehilangan role-nya.", 409)
		}
	}
	patch := map[string]any{"username": username, "email": email}
	if in.Name != nil {
		patch["name"] = JSTrim(*in.Name)
	}
	if in.Status != nil {
		patch["status"] = *in.Status
	}
	if e = c.accounts.UpdateAdminAccount(ctx, id, patch); e != nil {
		return nil, e
	}
	if in.RoleIDs != nil {
		if e = c.accounts.ReplaceAccountRoles(ctx, id, newIDs, actor, time.Now().UTC()); e != nil {
			return nil, e
		}
	}
	return c.GetAccount(ctx, raw)
}
func (c *AdminAccessController) DeleteAccount(ctx context.Context, raw string, actor bson.ObjectID) (map[string]any, error) {
	id, e := bson.ObjectIDFromHex(raw)
	if e != nil {
		return nil, accessFailure("USRB02001", "ID admin tidak valid.", 400)
	}
	if id == actor {
		return nil, accessFailure("USRB02005", "Akun yang sedang digunakan tidak dapat dihapus.", 409)
	}
	a, e := c.accounts.FindAdminAccount(ctx, id)
	if e != nil {
		return nil, e
	}
	if a == nil || a.DeletedAt != nil {
		return nil, accessFailure("USRB02004", "Admin tidak ditemukan.", 404)
	}
	roles, e := c.roles.ListRoles(ctx)
	if e != nil {
		return nil, e
	}
	links, e := c.accounts.ListRoleLinks(ctx)
	if e != nil {
		return nil, e
	}
	ownsSuper := false
	for _, r := range roles {
		if r.IsSystem && r.Code == "SUPER_ADMIN" {
			for _, l := range links {
				ownsSuper = ownsSuper || (l.AdminAccountID == id && l.RoleID == r.ID && l.RevokedAt == nil)
			}
		}
	}
	if ownsSuper {
		count, e := c.countActiveSupers(ctx, roles, links)
		if e != nil {
			return nil, e
		}
		if count <= 1 {
			return nil, accessFailure("USRB02005", "Super Admin aktif terakhir tidak dapat dihapus.", 409)
		}
	}
	now := time.Now().UTC()
	if e = c.accounts.SoftDeleteAdminAccount(ctx, id, now); e != nil {
		return nil, e
	}
	if e = c.accounts.RevokeAccountRoles(ctx, id, now); e != nil {
		return nil, e
	}
	return map[string]any{"id": id.Hex(), "deletedAt": now}, nil
}

func (c *AdminAccessController) checkRoleUnique(ctx context.Context, name, code string, exclude bson.ObjectID) error {
	rows, e := c.roles.ListRoles(ctx)
	if e != nil {
		return e
	}
	for _, r := range rows {
		if r.ID != exclude && r.DeletedAt == nil && (strings.EqualFold(JSTrim(r.Name), JSTrim(name)) || strings.EqualFold(r.Code, code)) {
			return accessFailure("ROLB02003", "Nama atau kode role sudah digunakan.", 409)
		}
	}
	return nil
}
func (c *AdminAccessController) selectPermissions(ctx context.Context, ids []string) ([]model.Permission, error) {
	perms, e := c.roles.ListPermissions(ctx)
	if e != nil {
		return nil, e
	}
	selected := []model.Permission{}
	for _, raw := range ids {
		id, er := bson.ObjectIDFromHex(raw)
		if er != nil {
			return nil, accessFailure("ROLB02003", "Satu atau lebih permission tidak tersedia.", 400)
		}
		found := false
		for _, p := range perms {
			if p.ID == id && raw == p.ID.Hex() && p.IsActive {
				selected = append(selected, p)
				found = true
				break
			}
		}
		if !found {
			return nil, accessFailure("ROLB02003", "Satu atau lebih permission tidak tersedia.", 400)
		}
	}
	return selected, nil
}
func (c *AdminAccessController) validAccountRoleIDs(ctx context.Context, ids []string) ([]bson.ObjectID, error) {
	roles, e := c.roles.ListRoles(ctx)
	if e != nil {
		return nil, e
	}
	out := []bson.ObjectID{}
	for _, raw := range ids {
		id, er := bson.ObjectIDFromHex(raw)
		found := false
		if er == nil && raw == id.Hex() {
			for _, r := range roles {
				if r.ID == id && r.Status == "ACTIVE" && r.DeletedAt == nil {
					found = true
					break
				}
			}
		}
		if !found {
			return nil, accessFailure("USRB02003", "Satu atau lebih role tidak tersedia.", 400)
		}
		out = append(out, id)
	}
	return out, nil
}
func (c *AdminAccessController) countActiveSupers(ctx context.Context, roles []model.AdminRole, links []model.AccountRole) (int, error) {
	supers := map[bson.ObjectID]bool{}
	for _, r := range roles {
		if r.IsSystem && r.Code == "SUPER_ADMIN" && r.DeletedAt == nil {
			supers[r.ID] = true
		}
	}
	accounts, e := c.accounts.ListAdminAccounts(ctx)
	if e != nil {
		return 0, e
	}
	count := 0
	for _, a := range accounts {
		if a.Status != "ACTIVE" || a.DeletedAt != nil {
			continue
		}
		for _, l := range links {
			if l.AdminAccountID == a.ID && l.RevokedAt == nil && supers[l.RoleID] {
				count++
				break
			}
		}
	}
	return count, nil
}

func roleDTO(r model.AdminRole, links []model.RolePermission, perms []model.Permission, accounts []model.AccountRole) map[string]any {
	permissionIDs := map[bson.ObjectID]bool{}
	for _, l := range links {
		if l.RoleID == r.ID {
			permissionIDs[l.PermissionID] = true
		}
	}
	permissionCodes := map[string]bool{}
	for _, p := range perms {
		if permissionIDs[p.ID] {
			permissionCodes[p.Code] = true
		}
	}
	keys := []string{}
	for _, p := range permissionDefs {
		if permissionCodes[p.Code] {
			keys = append(keys, permissionKey(p.Code))
		}
	}
	count := 0
	for _, a := range accounts {
		if a.RoleID == r.ID && a.RevokedAt == nil {
			count++
		}
	}
	desc := ""
	if r.Description != nil {
		desc = *r.Description
	}
	return map[string]any{"id": r.ID.Hex(), "name": r.Name, "code": r.Code, "status": r.Status, "description": desc, "adminCount": count, "permissions": keys, "protected": r.IsSystem}
}
func permissionKey(code string) string {
	for i, p := range permissionDefs {
		if p.Code == code {
			return []string{"dashboard", "orders", "catalog", "users", "content", "finance", "settings"}[i]
		}
	}
	return ""
}
func filterRoles(rows []model.AdminRole) []model.AdminRole {
	out := rows[:0]
	for _, r := range rows {
		if r.DeletedAt == nil {
			out = append(out, r)
		}
	}
	return out
}
func roleStatus(s *string) string {
	if s == nil {
		return "ACTIVE"
	}
	return *s
}
func validRoleInput(in RoleInput, partial bool) bool {
	if partial && in.Name == nil && in.Code == nil && !in.Description.Set && in.Status == nil && in.Permissions == nil {
		return false
	}
	if !partial && (in.Name == nil || in.Code == nil) {
		return false
	}
	if in.Name != nil && (JSTrim(*in.Name) == "" || JSStringLength(JSTrim(*in.Name)) > 100) {
		return false
	}
	if in.Code != nil && !roleCodePattern.MatchString(*in.Code) {
		return false
	}
	if in.Description.Set && in.Description.Value != nil && JSStringLength(*in.Description.Value) > 500 {
		return false
	}
	if in.Status != nil && *in.Status != "ACTIVE" && *in.Status != "INACTIVE" {
		return false
	}
	if in.Permissions != nil {
		seen := map[string]bool{}
		for _, id := range *in.Permissions {
			if _, e := bson.ObjectIDFromHex(id); e != nil || seen[id] {
				return false
			}
			seen[id] = true
		}
	}
	return true
}
func validAdminAccount(in AdminAccountInput, create bool) bool {
	if create && (in.Name == nil || in.Username == nil || in.Email == nil || in.RoleIDs == nil || len(*in.RoleIDs) == 0) {
		return false
	}
	if !create && in.Name == nil && in.Username == nil && in.Email == nil && in.Status == nil && in.RoleIDs == nil {
		return false
	}
	if in.Name != nil && (JSTrim(*in.Name) == "" || JSStringLength(JSTrim(*in.Name)) > 150) {
		return false
	}
	if in.Username != nil && !memberUsernamePattern.MatchString(strings.ToLower(JSTrim(*in.Username))) {
		return false
	}
	if in.Email != nil && (!memberEmailPattern.MatchString(JSTrim(*in.Email)) || JSStringLength(*in.Email) > 254) {
		return false
	}
	if in.Status != nil && *in.Status != "ACTIVE" && *in.Status != "INACTIVE" {
		return false
	}
	if in.RoleIDs != nil {
		if create && len(*in.RoleIDs) == 0 {
			return false
		}
		seen := map[string]bool{}
		for _, id := range *in.RoleIDs {
			if _, e := bson.ObjectIDFromHex(id); e != nil || seen[id] {
				return false
			}
			seen[id] = true
		}
	}
	return true
}
func containsString(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
func firstString(xs []string) string {
	if len(xs) > 0 {
		return xs[0]
	}
	return ""
}

var _ = sort.Strings
