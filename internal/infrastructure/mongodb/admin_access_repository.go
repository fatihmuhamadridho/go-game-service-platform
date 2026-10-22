package mongodb

import (
	"context"
	"strings"
	"time"

	"example.com/learn-golang/internal/model"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type AdminAccessRepository struct{ db *mongo.Database }

func NewAdminAccessRepository(db *mongo.Database) *AdminAccessRepository {
	return &AdminAccessRepository{db: db}
}
func (r *AdminAccessRepository) ListRoles(ctx context.Context) ([]model.AdminRole, error) {
	return findAll[model.AdminRole](ctx, r.db.Collection("admin_role"), bson.D{})
}
func (r *AdminAccessRepository) FindRole(ctx context.Context, id bson.ObjectID) (*model.AdminRole, error) {
	var x model.AdminRole
	e := r.db.Collection("admin_role").FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Decode(&x)
	if e == mongo.ErrNoDocuments {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	return &x, nil
}
func (r *AdminAccessRepository) CreateRole(ctx context.Context, x model.AdminRole) error {
	_, e := r.db.Collection("admin_role").InsertOne(ctx, x)
	return e
}
func (r *AdminAccessRepository) UpdateRole(ctx context.Context, id bson.ObjectID, p map[string]any) error {
	if _, ok := p["updated_at"]; !ok {
		p["updated_at"] = time.Now().UTC()
	}
	_, e := r.db.Collection("admin_role").UpdateOne(ctx, bson.D{{Key: "_id", Value: id}, {Key: "deleted_at", Value: nil}}, bson.D{{Key: "$set", Value: p}})
	return e
}
func (r *AdminAccessRepository) SoftDeleteRole(ctx context.Context, id bson.ObjectID, at time.Time) error {
	_, e := r.db.Collection("admin_role").UpdateOne(ctx, bson.D{{Key: "_id", Value: id}, {Key: "deleted_at", Value: nil}}, bson.D{{Key: "$set", Value: bson.D{{Key: "deleted_at", Value: at}, {Key: "status", Value: "INACTIVE"}, {Key: "updated_at", Value: at}}}})
	return e
}
func (r *AdminAccessRepository) ListPermissions(ctx context.Context) ([]model.Permission, error) {
	return findAll[model.Permission](ctx, r.db.Collection("permission"), bson.D{})
}
func (r *AdminAccessRepository) EnsurePermissions(ctx context.Context, defs []model.Permission) ([]model.Permission, error) {
	rows, e := r.ListPermissions(ctx)
	if e != nil {
		return nil, e
	}
	byCode := map[string]model.Permission{}
	for _, x := range rows {
		byCode[x.Code] = x
	}
	now := time.Now().UTC()
	for _, d := range defs {
		if _, ok := byCode[d.Code]; ok {
			continue
		}
		d.ID = bson.NewObjectID()
		d.CreatedAt = now
		d.UpdatedAt = now
		d.IsActive = true
		if _, e = r.db.Collection("permission").InsertOne(ctx, d); e != nil {
			return nil, e
		}
		byCode[d.Code] = d
	}
	out := make([]model.Permission, 0, len(defs))
	for _, d := range defs {
		out = append(out, byCode[d.Code])
	}
	return out, nil
}
func (r *AdminAccessRepository) ListRolePermissions(ctx context.Context) ([]model.RolePermission, error) {
	return findAll[model.RolePermission](ctx, r.db.Collection("role_permission"), bson.D{})
}
func (r *AdminAccessRepository) ReplaceRolePermissions(ctx context.Context, id bson.ObjectID, selected []model.Permission, actor bson.ObjectID, at time.Time) error {
	_, e := r.db.Collection("role_permission").DeleteMany(ctx, bson.D{{Key: "role_id", Value: id}})
	if e != nil {
		return e
	}
	for _, p := range selected {
		var by *bson.ObjectID
		if !actor.IsZero() {
			by = &actor
		}
		if _, e = r.db.Collection("role_permission").InsertOne(ctx, model.RolePermission{ID: bson.NewObjectID(), RoleID: id, PermissionID: p.ID, GrantedBy: by, CreatedAt: at}); e != nil {
			return e
		}
	}
	return nil
}
func (r *AdminAccessRepository) ListAccountRoles(ctx context.Context) ([]model.AccountRole, error) {
	return findAll[model.AccountRole](ctx, r.db.Collection("account_role"), bson.D{})
}
func (r *AdminAccessRepository) CountAssignedAccountsForRole(ctx context.Context, id bson.ObjectID) (int, error) {
	count, err := r.db.Collection("account_role").CountDocuments(ctx, bson.D{{Key: "role_id", Value: id}, {Key: "revoked_at", Value: nil}})
	return int(count), err
}
func (r *AdminAccessRepository) RecordAdminRoleActivity(ctx context.Context, audit model.ActivityContext, action string, id bson.ObjectID, changed map[string]any) error {
	var account model.Account
	var actorID, actorName, actorEmail any
	if !audit.ActorID.IsZero() {
		if e := r.db.Collection("admin_account").FindOne(ctx, bson.D{{Key: "_id", Value: audit.ActorID}}).Decode(&account); e == nil {
			actorID = audit.ActorID
			actorName = account.Name
			actorEmail = account.Email
		}
	}
	_, e := r.db.Collection("activity_log").InsertOne(ctx, bson.D{{Key: "_id", Value: bson.NewObjectID()}, {Key: "actor_admin_id", Value: actorID}, {Key: "actor_name_snapshot", Value: actorName}, {Key: "actor_email_snapshot", Value: actorEmail}, {Key: "action_code", Value: action}, {Key: "action_label", Value: strings.ToLower(strings.ReplaceAll(action, "_", " "))}, {Key: "category", Value: "SETTINGS"}, {Key: "target_type", Value: "ADMIN_ROLE"}, {Key: "target_id", Value: id}, {Key: "target_reference", Value: id.Hex()}, {Key: "result", Value: "SUCCESS"}, {Key: "failure_code", Value: nil}, {Key: "reason", Value: nil}, {Key: "changed_fields", Value: changed}, {Key: "request_id", Value: optionalValue(audit.RequestID)}, {Key: "source_ip", Value: optionalValue(audit.SourceIP)}, {Key: "user_agent", Value: optionalValue(audit.UserAgent)}, {Key: "occurred_at", Value: time.Now().UTC()}})
	return e
}

func (r *AdminAccessRepository) ListAdminAccounts(ctx context.Context) ([]model.Account, error) {
	return findAll[model.Account](ctx, r.db.Collection("admin_account"), bson.D{})
}
func (r *AdminAccessRepository) FindAdminAccount(ctx context.Context, id bson.ObjectID) (*model.Account, error) {
	var x model.Account
	e := r.db.Collection("admin_account").FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Decode(&x)
	if e == mongo.ErrNoDocuments {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	return &x, nil
}
func (r *AdminAccessRepository) FindAdminAccountByUsernameOrEmail(ctx context.Context, u, mail string, excluding ...bson.ObjectID) (*model.Account, error) {
	var x model.Account
	conditions := bson.A{}
	if u != "" {
		conditions = append(conditions, bson.D{{Key: "username", Value: u}})
	}
	if mail != "" {
		conditions = append(conditions, bson.D{{Key: "email", Value: mail}})
	}
	filter := bson.D{}
	if len(conditions) == 1 {
		filter = conditions[0].(bson.D)
	} else if len(conditions) > 1 {
		filter = append(filter, bson.E{Key: "$or", Value: conditions})
	}
	if len(excluding) > 0 {
		filter = append(filter, bson.E{Key: "_id", Value: bson.D{{Key: "$ne", Value: excluding[0]}}})
	}
	e := r.db.Collection("admin_account").FindOne(ctx, filter).Decode(&x)
	if e == mongo.ErrNoDocuments {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	return &x, nil
}
func (r *AdminAccessRepository) UpdateAdminAccount(ctx context.Context, id bson.ObjectID, p map[string]any) error {
	p["updated_at"] = time.Now().UTC()
	_, e := r.db.Collection("admin_account").UpdateOne(ctx, bson.D{{Key: "_id", Value: id}, {Key: "deleted_at", Value: nil}}, bson.D{{Key: "$set", Value: p}})
	return e
}
func (r *AdminAccessRepository) SoftDeleteAdminAccount(ctx context.Context, id bson.ObjectID, at time.Time) error {
	_, e := r.db.Collection("admin_account").UpdateOne(ctx, bson.D{{Key: "_id", Value: id}, {Key: "deleted_at", Value: nil}}, bson.D{{Key: "$set", Value: bson.D{{Key: "status", Value: "INACTIVE"}, {Key: "deleted_at", Value: at}, {Key: "updated_at", Value: at}}}})
	return e
}
func (r *AdminAccessRepository) ListRoleLinks(ctx context.Context) ([]model.AccountRole, error) {
	return r.ListAccountRoles(ctx)
}
func (r *AdminAccessRepository) ReplaceAccountRoles(ctx context.Context, id bson.ObjectID, roleIDs []bson.ObjectID, actor bson.ObjectID, at time.Time) error {
	links, e := r.ListAccountRoles(ctx)
	if e != nil {
		return e
	}
	wanted := map[bson.ObjectID]bool{}
	for _, rid := range roleIDs {
		wanted[rid] = true
	}
	seen := map[bson.ObjectID]bool{}
	for _, l := range links {
		if l.AdminAccountID != id {
			continue
		}
		if wanted[l.RoleID] {
			if l.RevokedAt != nil {
				_, e = r.db.Collection("account_role").UpdateOne(ctx, bson.D{{Key: "_id", Value: l.ID}}, bson.D{{Key: "$set", Value: bson.D{{Key: "revoked_at", Value: nil}, {Key: "assigned_at", Value: at}}}})
				if e != nil {
					return e
				}
			}
			seen[l.RoleID] = true
		} else if l.RevokedAt == nil {
			_, e = r.db.Collection("account_role").UpdateOne(ctx, bson.D{{Key: "_id", Value: l.ID}}, bson.D{{Key: "$set", Value: bson.D{{Key: "revoked_at", Value: at}}}})
			if e != nil {
				return e
			}
		}
	}
	for rid := range wanted {
		if !seen[rid] {
			var by *bson.ObjectID
			if !actor.IsZero() {
				by = &actor
			}
			if _, e = r.db.Collection("account_role").InsertOne(ctx, model.AccountRole{ID: bson.NewObjectID(), AdminAccountID: id, RoleID: rid, AssignedBy: by, AssignedAt: at}); e != nil {
				return e
			}
		}
	}
	return nil
}
func (r *AdminAccessRepository) RevokeAccountRoles(ctx context.Context, id bson.ObjectID, at time.Time) error {
	_, e := r.db.Collection("account_role").UpdateMany(ctx, bson.D{{Key: "admin_account_id", Value: id}, {Key: "revoked_at", Value: nil}}, bson.D{{Key: "$set", Value: bson.D{{Key: "revoked_at", Value: at}}}})
	return e
}

var _ model.AdminRoleRepository = (*AdminAccessRepository)(nil)
var _ model.AdminAccountRepository = (*AdminAccessRepository)(nil)
