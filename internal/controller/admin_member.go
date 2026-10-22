package controller

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"example.com/learn-golang/internal/model"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type AdminMemberError struct {
	Code, Message string
	Status        int
}

func (e *AdminMemberError) Error() string { return e.Message }
func ToAdminMemberError(err error) *AdminMemberError {
	var target *AdminMemberError
	if errors.As(err, &target) {
		return target
	}
	return &AdminMemberError{Code: "USRT03001", Message: "User sedang tidak tersedia.", Status: 500}
}

type AdminMemberController struct{ repo model.AdminMemberRepository }

func NewAdminMemberController(repo model.AdminMemberRepository) *AdminMemberController {
	return &AdminMemberController{repo: repo}
}

type MemberInput struct {
	Name, Username, Email *string
	WhatsApp              model.OptionalString
	Status                *string
}
type MemberDTO struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Username    string    `json:"username"`
	Email       string    `json:"email"`
	WhatsApp    *string   `json:"whatsapp"`
	Status      string    `json:"status"`
	AccountType string    `json:"accountType"`
	Tier        string    `json:"tier"`
	JoinedAt    time.Time `json:"joinedAt"`
	OrderCount  int       `json:"orderCount"`
	TotalSpend  float64   `json:"totalSpend"`
}
type MemberListDTO struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Username    string    `json:"username"`
	Email       string    `json:"email"`
	Status      string    `json:"status"`
	AccountType string    `json:"accountType"`
	Tier        string    `json:"tier"`
	JoinedAt    time.Time `json:"joinedAt"`
	OrderCount  int       `json:"orderCount"`
	TotalSpend  float64   `json:"totalSpend"`
}

var memberUsernamePattern = regexp.MustCompile(`^[a-z0-9._-]{3,30}$`)
var memberEmailPattern = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)
var memberWhatsAppPattern = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)

func (c *AdminMemberController) List(ctx context.Context, page, pageSize int, status, search string) (map[string]any, error) {
	users, err := c.repo.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	search = strings.ToLower(search)
	rows := make([]MemberListDTO, 0, len(users))
	for _, u := range users {
		if u.DeletedAt != nil || (status != "" && u.Status != status) {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(u.Name+" "+u.Username+" "+u.Email), search) {
			continue
		}
		joinedAt := u.JoinedAt
		if joinedAt.IsZero() {
			joinedAt = u.CreatedAt
		}
		rows = append(rows, MemberListDTO{ID: u.ID.Hex(), Name: u.Name, Username: u.Username, Email: u.Email, Status: u.Status, AccountType: "MEMBER", JoinedAt: joinedAt})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].JoinedAt.Equal(rows[j].JoinedAt) {
			return rows[i].ID > rows[j].ID
		}
		return rows[i].JoinedAt.After(rows[j].JoinedAt)
	})
	total := len(rows)
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	profiles, err := c.repo.ListMemberProfiles(ctx)
	if err != nil {
		return nil, err
	}
	orders, err := c.repo.ListAllMemberOrders(ctx)
	if err != nil {
		return nil, err
	}
	profileByUser := make(map[bson.ObjectID]model.MemberProfile, len(profiles))
	for _, profile := range profiles {
		profileByUser[profile.UserID] = profile
	}
	ordersByUser := make(map[bson.ObjectID][]model.Order)
	for _, order := range orders {
		if order.UserID != nil {
			ordersByUser[*order.UserID] = append(ordersByUser[*order.UserID], order)
		}
	}
	pageRows := rows[start:end]
	for i := range pageRows {
		userID, _ := bson.ObjectIDFromHex(pageRows[i].ID)
		if profile, ok := profileByUser[userID]; ok && profile.Tier != "" {
			pageRows[i].Tier = profile.Tier
		} else {
			pageRows[i].Tier = "BRONZE"
		}
		userOrders := ordersByUser[userID]
		pageRows[i].OrderCount = len(userOrders)
		for _, order := range userOrders {
			if order.PaymentStatus == "PAID" {
				spend, _ := strconv.ParseFloat(order.TotalAmount.String(), 64)
				pageRows[i].TotalSpend += spend
			}
		}
	}
	return map[string]any{"users": pageRows, "pagination": map[string]int{"page": page, "pageSize": pageSize, "totalItems": total, "totalPages": (total + pageSize - 1) / pageSize}}, nil
}
func (c *AdminMemberController) Get(ctx context.Context, rawID string) (MemberDTO, error) {
	id, err := bson.ObjectIDFromHex(rawID)
	if err != nil {
		return MemberDTO{}, memberFailure("USRB02004", "User tidak ditemukan.", 404)
	}
	u, err := c.repo.FindUserByID(ctx, id)
	if err != nil {
		return MemberDTO{}, err
	}
	if u == nil || u.DeletedAt != nil {
		return MemberDTO{}, memberFailure("USRB02004", "User tidak ditemukan.", 404)
	}
	return c.memberDTO(ctx, *u)
}
func (c *AdminMemberController) Create(ctx context.Context, in MemberInput) error {
	if !validMemberInput(in, true) {
		return memberFailure("USRB02001", "Periksa nama, username, dan email member.", 400)
	}
	username, email := strings.ToLower(JSTrim(*in.Username)), strings.ToLower(JSTrim(*in.Email))
	exists, err := c.repo.FindUserByUsernameOrEmail(ctx, username, email)
	if err != nil {
		return err
	}
	if exists != nil {
		return memberFailure("USRB02002", "Username atau email sudah digunakan.", 409)
	}
	return memberFailure("USRT02001", "Undangan user belum tersedia karena layanan email/identity belum dikonfigurasi.", 503)
}
func (c *AdminMemberController) Update(ctx context.Context, rawID string, in MemberInput, actor bson.ObjectID) (map[string]any, error) {
	if !validMemberInput(in, false) {
		return nil, memberFailure("USRB02001", "Data user tidak valid.", 400)
	}
	id, err := bson.ObjectIDFromHex(rawID)
	if err != nil {
		return nil, memberFailure("USRB02004", "User tidak ditemukan.", 404)
	}
	u, err := c.repo.FindUserByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if u == nil || u.DeletedAt != nil {
		return nil, memberFailure("USRB02004", "User tidak ditemukan.", 404)
	}
	username, email := u.Username, u.Email
	patch := map[string]any{}
	if in.Name != nil {
		patch["name"] = JSTrim(*in.Name)
	}
	if in.Username != nil {
		username = strings.ToLower(JSTrim(*in.Username))
		patch["username"] = username
	}
	if in.Email != nil {
		email = strings.ToLower(JSTrim(*in.Email))
		patch["email"] = email
	}
	if in.WhatsApp.Set {
		patch["whatsapp"] = in.WhatsApp.Value
	}
	if in.Status != nil {
		patch["status"] = *in.Status
	}
	if in.Username != nil || in.Email != nil {
		usernameToCheck, emailToCheck := "", ""
		if in.Username != nil && username != u.Username {
			usernameToCheck = username
		}
		if in.Email != nil && email != u.Email {
			emailToCheck = email
		}
		other, e := c.repo.FindUserByUsernameOrEmail(ctx, usernameToCheck, emailToCheck)
		if e != nil {
			return nil, e
		}
		if other != nil && other.ID != id {
			return nil, memberFailure("USRB02002", "Username atau email sudah digunakan.", 409)
		}
	}
	if err = c.repo.UpdateUser(ctx, id, patch); err != nil {
		return nil, err
	}
	if in.Status != nil && *in.Status != u.Status {
		history := model.UserStatusHistory{ID: bson.NewObjectID(), UserID: id, OldStatus: &u.Status, NewStatus: *in.Status, ChangedAt: time.Now().UTC()}
		if !actor.IsZero() {
			history.ChangedBy = &actor
		}
		if err = c.repo.CreateUserStatusHistory(ctx, history); err != nil {
			return nil, err
		}
	}
	updated, err := c.repo.FindUserByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return map[string]any{"user": map[string]any{"id": id.Hex(), "name": updated.Name, "username": updated.Username, "email": updated.Email, "status": updated.Status}}, nil
}
func (c *AdminMemberController) Delete(ctx context.Context, rawID string) (map[string]any, error) {
	id, err := bson.ObjectIDFromHex(rawID)
	if err != nil {
		return nil, memberFailure("USRB02004", "User tidak ditemukan.", 404)
	}
	u, err := c.repo.FindUserByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if u == nil || u.DeletedAt != nil {
		return nil, memberFailure("USRB02004", "User tidak ditemukan.", 404)
	}
	now := time.Now().UTC()
	if err = c.repo.UpdateUser(ctx, id, map[string]any{"deleted_at": now, "status": "BLOCKED"}); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true}, nil
}
func (c *AdminMemberController) memberDTO(ctx context.Context, u model.Account) (MemberDTO, error) {
	profile, err := c.repo.FindMemberProfile(ctx, u.ID)
	if err != nil {
		return MemberDTO{}, err
	}
	tier := "BRONZE"
	if profile != nil && profile.Tier != "" {
		tier = profile.Tier
	}
	orders, err := c.repo.ListOrdersByUser(ctx, u.ID)
	if err != nil {
		return MemberDTO{}, err
	}
	spend := 0.0
	count := len(orders)
	for _, o := range orders {
		if o.PaymentStatus == "PAID" {
			f, _ := strconv.ParseFloat(o.TotalAmount.String(), 64)
			spend += f
		}
	}
	joined := u.JoinedAt
	if joined.IsZero() {
		joined = u.CreatedAt
	}
	return MemberDTO{ID: u.ID.Hex(), Name: u.Name, Username: u.Username, Email: u.Email, WhatsApp: u.WhatsApp, Status: u.Status, AccountType: "MEMBER", Tier: tier, JoinedAt: joined, OrderCount: count, TotalSpend: spend}, nil
}
func validMemberInput(in MemberInput, create bool) bool {
	if create {
		if in.Name == nil || in.Username == nil || in.Email == nil {
			return false
		}
		if JSTrim(*in.Name) == "" || JSStringLength(JSTrim(*in.Name)) > 150 || !memberUsernamePattern.MatchString(strings.ToLower(JSTrim(*in.Username))) || JSStringLength(*in.Email) > 254 || !memberEmailPattern.MatchString(JSTrim(*in.Email)) {
			return false
		}
		return true
	}
	if in.Name == nil && in.Username == nil && in.Email == nil && !in.WhatsApp.Set && in.Status == nil {
		return false
	}
	if in.Name != nil && (JSTrim(*in.Name) == "" || JSStringLength(JSTrim(*in.Name)) > 150) {
		return false
	}
	if in.Username != nil && !memberUsernamePattern.MatchString(strings.ToLower(JSTrim(*in.Username))) {
		return false
	}
	if in.Email != nil && (JSStringLength(*in.Email) > 254 || !memberEmailPattern.MatchString(JSTrim(*in.Email))) {
		return false
	}
	if in.WhatsApp.Set && in.WhatsApp.Value != nil && !memberWhatsAppPattern.MatchString(*in.WhatsApp.Value) {
		return false
	}
	if in.Status != nil && *in.Status != "ACTIVE" && *in.Status != "BLOCKED" {
		return false
	}
	return true
}
func memberFailure(code, message string, status int) error {
	return &AdminMemberError{Code: code, Message: message, Status: status}
}
