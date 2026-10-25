package role

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	roleCollection       = "admin_role"
	permissionCollection = "permission"
	grantCollection      = "role_permission"
	assignmentCollection = "account_role"
	accountCollection    = "admin_account"
)

type MongoRepository struct{ database *mongo.Database }

func NewMongoRepository(database *mongo.Database) *MongoRepository {
	return &MongoRepository{database: database}
}

func (r *MongoRepository) List(ctx context.Context, q Query) (Page, error) {
	filter := bson.D{{Key: "deleted_at", Value: nil}}
	if q.Status != "" {
		filter = append(filter, bson.E{Key: "status", Value: q.Status})
	}
	if q.Search != "" {
		pattern := ".*" + regexp.QuoteMeta(q.Search) + ".*"
		filter = append(filter, bson.E{Key: "$or", Value: bson.A{bson.D{{Key: "name", Value: bson.Regex{Pattern: pattern, Options: "i"}}}, bson.D{{Key: "code", Value: bson.Regex{Pattern: pattern, Options: "i"}}}}})
	}
	collection := r.database.Collection(roleCollection)
	total, err := collection.CountDocuments(ctx, filter)
	if err != nil {
		return Page{}, err
	}
	sortField := map[string]string{"name": "name", "createdAt": "created_at", "updatedAt": "updated_at"}[q.SortBy]
	direction := 1
	if q.SortOrder == "DESC" {
		direction = -1
	}
	cursor, err := collection.Find(ctx, filter, options.Find().SetSort(bson.D{{Key: sortField, Value: direction}, {Key: "role_id", Value: 1}}).SetSkip(int64(q.Page-1)*int64(q.Limit)).SetLimit(int64(q.Limit)))
	if err != nil {
		return Page{}, err
	}
	defer cursor.Close(ctx)
	var roles []Role
	if err = cursor.All(ctx, &roles); err != nil {
		return Page{}, err
	}
	if roles == nil {
		roles = []Role{}
	}
	if err = r.attachPermissions(ctx, roles); err != nil {
		return Page{}, err
	}
	pages := 0
	if total > 0 {
		pages = int((total + int64(q.Limit) - 1) / int64(q.Limit))
	}
	return Page{Roles: roles, Pagination: Pagination{Page: q.Page, Limit: q.Limit, Total: total, TotalPages: pages}}, nil
}
func (r *MongoRepository) Detail(ctx context.Context, id string) (Role, error) {
	var result Role
	err := r.database.Collection(roleCollection).FindOne(ctx, bson.D{{Key: "role_id", Value: id}, {Key: "status", Value: "ACTIVE"}, {Key: "deleted_at", Value: nil}}).Decode(&result)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Role{}, ErrNotFound
	}
	if err != nil {
		return Role{}, err
	}
	roles := []Role{result}
	if err = r.attachPermissions(ctx, roles); err != nil {
		return Role{}, err
	}
	return roles[0], nil
}

func (r *MongoRepository) attachPermissions(ctx context.Context, roles []Role) error {
	if len(roles) == 0 {
		return nil
	}
	ids := make(bson.A, 0, len(roles))
	for _, role := range roles {
		ids = append(ids, role.ID)
	}
	var links []struct {
		RoleID       string `bson:"role_id"`
		PermissionID string `bson:"permission_id"`
	}
	cur, err := r.database.Collection(grantCollection).Find(ctx, bson.D{{Key: "role_id", Value: bson.D{{Key: "$in", Value: ids}}}})
	if err != nil {
		return err
	}
	if err = cur.All(ctx, &links); err != nil {
		return err
	}
	permissionIDs := make(bson.A, 0, len(links))
	seenPermissionIDs := make(map[string]bool, len(links))
	for _, link := range links {
		if !seenPermissionIDs[link.PermissionID] {
			permissionIDs = append(permissionIDs, link.PermissionID)
			seenPermissionIDs[link.PermissionID] = true
		}
	}
	keys := map[string]string{}
	if len(permissionIDs) > 0 {
		var catalog []struct {
			ID  string `bson:"permission_id"`
			Key string `bson:"permission_key"`
		}
		cur, err = r.database.Collection(permissionCollection).Find(ctx, bson.D{{Key: "permission_id", Value: bson.D{{Key: "$in", Value: permissionIDs}}}})
		if err != nil {
			return err
		}
		if err = cur.All(ctx, &catalog); err != nil {
			return err
		}
		for _, permission := range catalog {
			keys[permission.ID] = permission.Key
		}
	}
	byRole := map[string][]string{}
	for _, link := range links {
		if key, ok := keys[link.PermissionID]; ok {
			byRole[link.RoleID] = append(byRole[link.RoleID], key)
		}
	}
	for index := range roles {
		roles[index].Permissions = byRole[roles[index].ID]
		if roles[index].Permissions == nil {
			roles[index].Permissions = []string{}
		}
		sort.Strings(roles[index].Permissions)
	}
	return nil
}

func (r *MongoRepository) validatePermissionKeys(ctx context.Context, keys []string) ([]string, error) {
	if len(keys) == 0 {
		return []string{}, nil
	}
	cursor, err := r.database.Collection(permissionCollection).Find(ctx, bson.D{{Key: "permission_key", Value: bson.D{{Key: "$in", Value: keys}}}, {Key: "is_active", Value: true}})
	if err != nil {
		return nil, err
	}
	var docs []struct {
		ID  string `bson:"permission_id"`
		Key string `bson:"permission_key"`
	}
	if err = cursor.All(ctx, &docs); err != nil {
		return nil, err
	}
	if len(docs) != len(keys) {
		return nil, ErrPermission
	}
	ids := make([]string, 0, len(docs))
	for _, doc := range docs {
		ids = append(ids, doc.ID)
	}
	return ids, nil
}
func (r *MongoRepository) Create(ctx context.Context, role Role) error {
	session, err := r.database.Client().StartSession()
	if err != nil {
		return err
	}
	defer session.EndSession(ctx)
	_, err = session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		ids, e := r.validatePermissionKeys(tx, role.Permissions)
		if e != nil {
			return nil, e
		}
		if _, e = r.database.Collection(roleCollection).InsertOne(tx, role); e != nil {
			if isDuplicate(e) {
				return nil, ErrConflict
			}
			return nil, e
		}
		for _, id := range ids {
			if _, e = r.database.Collection(grantCollection).InsertOne(tx, bson.D{{Key: "role_id", Value: role.ID}, {Key: "permission_id", Value: id}, {Key: "created_at", Value: role.CreatedAt}}); e != nil {
				return nil, e
			}
		}
		return nil, nil
	})
	return err
}
func (r *MongoRepository) Update(ctx context.Context, id string, c Changes, now time.Time) error {
	session, err := r.database.Client().StartSession()
	if err != nil {
		return err
	}
	defer session.EndSession(ctx)
	_, err = session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		var current Role
		e := r.database.Collection(roleCollection).FindOne(tx, bson.D{{Key: "role_id", Value: id}, {Key: "deleted_at", Value: nil}}).Decode(&current)
		if errors.Is(e, mongo.ErrNoDocuments) {
			return nil, ErrNotFound
		}
		if e != nil {
			return nil, e
		}
		if current.IsSystem || current.Code == "SUPER_ADMIN" {
			return nil, ErrProtected
		}
		set := bson.D{}
		if c.Name != nil && *c.Name != current.Name {
			set = append(set, bson.E{Key: "name", Value: *c.Name})
		}
		if c.Code != nil && *c.Code != current.Code {
			set = append(set, bson.E{Key: "code", Value: *c.Code})
		}
		if c.Description != nil && !equalDescription(*c.Description, current.Description) {
			set = append(set, bson.E{Key: "description", Value: *c.Description})
		}
		if c.Status != nil && *c.Status != current.Status {
			if *c.Status == "INACTIVE" {
				assigned, e := r.hasActiveAssignments(tx, id)
				if e != nil {
					return nil, e
				}
				if assigned {
					return nil, ErrAssigned
				}
			}
			set = append(set, bson.E{Key: "status", Value: *c.Status})
		}
		var permissionIDs []string
		replacePermissions := false
		if c.Permissions != nil {
			permissionIDs, e = r.validatePermissionKeys(tx, *c.Permissions)
			if e != nil {
				return nil, e
			}
			existing, e := r.permissionIDs(tx, id)
			if e != nil {
				return nil, e
			}
			if !sameStrings(existing, permissionIDs) {
				replacePermissions = true
			}
		}
		if len(set) == 0 && !replacePermissions {
			return nil, nil
		}
		set = append(set, bson.E{Key: "updated_at", Value: now})
		res, e := r.database.Collection(roleCollection).UpdateOne(tx, bson.D{{Key: "role_id", Value: id}, {Key: "deleted_at", Value: nil}}, bson.D{{Key: "$set", Value: set}})
		if e != nil {
			if isDuplicate(e) {
				return nil, ErrConflict
			}
			return nil, e
		}
		if res.MatchedCount != 1 {
			return nil, ErrNotFound
		}
		if replacePermissions {
			if _, e = r.database.Collection(grantCollection).DeleteMany(tx, bson.D{{Key: "role_id", Value: id}}); e != nil {
				return nil, e
			}
			for _, permissionID := range permissionIDs {
				if _, e = r.database.Collection(grantCollection).InsertOne(tx, bson.D{{Key: "role_id", Value: id}, {Key: "permission_id", Value: permissionID}, {Key: "created_at", Value: now}}); e != nil {
					return nil, e
				}
			}
		}
		return nil, nil
	})
	return err
}
func equalDescription(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	sort.Strings(left)
	sort.Strings(right)
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
func (r *MongoRepository) permissionIDs(ctx context.Context, id string) ([]string, error) {
	cur, err := r.database.Collection(grantCollection).Find(ctx, bson.D{{Key: "role_id", Value: id}})
	if err != nil {
		return nil, err
	}
	var links []struct {
		ID string `bson:"permission_id"`
	}
	if err = cur.All(ctx, &links); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(links))
	for _, link := range links {
		ids = append(ids, link.ID)
	}
	return ids, nil
}
func (r *MongoRepository) hasActiveAssignments(ctx context.Context, id string) (bool, error) {
	pipeline := mongo.Pipeline{{{Key: "$match", Value: bson.D{{Key: "role_id", Value: id}, {Key: "revoked_at", Value: nil}}}}, {{Key: "$lookup", Value: bson.D{{Key: "from", Value: accountCollection}, {Key: "localField", Value: "admin_account_id"}, {Key: "foreignField", Value: "admin_account_id"}, {Key: "as", Value: "account"}}}}, {{Key: "$match", Value: bson.D{{Key: "account.status", Value: "ACTIVE"}, {Key: "account.deleted_at", Value: nil}}}}, {{Key: "$limit", Value: 1}}}
	cursor, err := r.database.Collection(assignmentCollection).Aggregate(ctx, pipeline)
	if err != nil {
		return false, err
	}
	defer cursor.Close(ctx)
	return cursor.Next(ctx), cursor.Err()
}
func (r *MongoRepository) Delete(ctx context.Context, id string, now time.Time) error {
	session, err := r.database.Client().StartSession()
	if err != nil {
		return err
	}
	defer session.EndSession(ctx)
	_, err = session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		var current Role
		e := r.database.Collection(roleCollection).FindOne(tx, bson.D{{Key: "role_id", Value: id}}).Decode(&current)
		if errors.Is(e, mongo.ErrNoDocuments) {
			return nil, ErrNotFound
		}
		if e != nil {
			return nil, e
		}
		if current.IsSystem || current.Code == "SUPER_ADMIN" {
			return nil, ErrProtected
		}
		if current.DeletedAt != nil {
			return nil, nil
		}
		assigned, e := r.hasActiveAssignments(tx, id)
		if e != nil {
			return nil, e
		}
		if assigned {
			return nil, ErrAssigned
		}
		_, e = r.database.Collection(roleCollection).UpdateOne(tx, bson.D{{Key: "role_id", Value: id}, {Key: "deleted_at", Value: nil}}, bson.D{{Key: "$set", Value: bson.D{{Key: "deleted_at", Value: now}, {Key: "updated_at", Value: now}}}})
		return nil, e
	})
	return err
}
func isDuplicate(err error) bool {
	var write mongo.WriteException
	var command mongo.CommandError
	return errors.As(err, &write) && len(write.WriteErrors) > 0 && write.WriteErrors[0].Code == 11000 || errors.As(err, &command) && command.Code == 11000
}
