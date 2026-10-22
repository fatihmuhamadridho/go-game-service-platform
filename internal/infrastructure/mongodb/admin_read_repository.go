package mongodb

import (
	"context"
	"strings"
	"time"

	"example.com/learn-golang/internal/model"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type AdminReadRepository struct{ db *mongo.Database }

func NewAdminReadRepository(db *mongo.Database) *AdminReadRepository {
	return &AdminReadRepository{db: db}
}
func (r *AdminReadRepository) ListTransactions(c context.Context) ([]model.FinancialTransaction, error) {
	return findAll[model.FinancialTransaction](c, r.db.Collection("financial_transaction"), bson.D{})
}
func (r *AdminReadRepository) FindTransaction(c context.Context, id bson.ObjectID) (*model.FinancialTransaction, error) {
	var x model.FinancialTransaction
	e := r.db.Collection("financial_transaction").FindOne(c, bson.D{{Key: "_id", Value: id}}).Decode(&x)
	if e == mongo.ErrNoDocuments {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	return &x, nil
}
func (r *AdminReadRepository) ListTransactionHistory(c context.Context, id bson.ObjectID) ([]model.TransactionStatusHistory, error) {
	return findAll[model.TransactionStatusHistory](c, r.db.Collection("transaction_status_history"), bson.D{{Key: "transaction_id", Value: id}})
}
func (r *AdminReadRepository) ListActivity(c context.Context) ([]model.ActivityLog, error) {
	return findAll[model.ActivityLog](c, r.db.Collection("activity_log"), bson.D{})
}
func (r *AdminReadRepository) ListAllOrders(c context.Context) ([]model.Order, error) {
	return findAll[model.Order](c, r.db.Collection("customer_order"), bson.D{})
}
func (r *AdminReadRepository) ListAllOrderItems(c context.Context) ([]model.OrderItem, error) {
	return findAll[model.OrderItem](c, r.db.Collection("order_item"), bson.D{})
}
func (r *AdminReadRepository) ListAllUsers(c context.Context) ([]model.Account, error) {
	return findAll[model.Account](c, r.db.Collection("user_account"), bson.D{})
}
func (r *AdminReadRepository) ListAllGameServices(c context.Context) ([]model.GameService, error) {
	return findAll[model.GameService](c, r.db.Collection("game_service"), bson.D{})
}
func (r *AdminReadRepository) UpsertSettings(c context.Context, s model.PlatformSettings) error {
	s.UpdatedAt = time.Now().UTC()
	_, e := r.db.Collection("platform_settings").UpdateOne(c, bson.D{{Key: "singleton_key", Value: "current"}}, bson.D{{Key: "$set", Value: bson.D{{Key: "site_name", Value: s.SiteName}, {Key: "site_description", Value: s.SiteDescription}, {Key: "contact_email", Value: s.ContactEmail}, {Key: "contact_whatsapp", Value: s.ContactWhatsApp}, {Key: "social_links", Value: s.SocialLinks}, {Key: "instagram_url", Value: s.InstagramURL}, {Key: "business_hours", Value: s.BusinessHours}, {Key: "maintenance_mode", Value: s.MaintenanceMode}, {Key: "allow_registration", Value: s.AllowRegistration}, {Key: "default_currency", Value: s.DefaultCurrency}, {Key: "timezone", Value: s.Timezone}, {Key: "version", Value: s.Version}, {Key: "updated_by", Value: s.UpdatedBy}, {Key: "updated_at", Value: s.UpdatedAt}}}, {Key: "$setOnInsert", Value: bson.D{{Key: "_id", Value: bson.NewObjectID()}, {Key: "singleton_key", Value: "current"}, {Key: "created_at", Value: s.UpdatedAt}}}}, options.UpdateOne().SetUpsert(true))
	return e
}
func (r *AdminReadRepository) ListSettings(c context.Context) (*model.PlatformSettings, error) {
	var s model.PlatformSettings
	e := r.db.Collection("platform_settings").FindOne(c, bson.D{{Key: "singleton_key", Value: "current"}}).Decode(&s)
	if e == mongo.ErrNoDocuments {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	return &s, nil
}

func (r *AdminReadRepository) RecordSettingsActivity(c context.Context, actorID bson.ObjectID, settings model.PlatformSettings, request model.SettingsActivityContext) error {
	var account model.Account
	var actor, name, email any
	if !actorID.IsZero() {
		if err := r.db.Collection("admin_account").FindOne(c, bson.D{{Key: "_id", Value: actorID}}).Decode(&account); err == nil {
			actor, name, email = account.ID, account.Name, account.Email
		}
	}
	label := "Memperbarui pengaturan platform"
	changed := bson.D{
		{Key: "siteName", Value: settings.SiteName}, {Key: "siteDescription", Value: settings.SiteDescription},
		{Key: "contactEmail", Value: settings.ContactEmail}, {Key: "contactWhatsapp", Value: settings.ContactWhatsApp},
		{Key: "socialLinks", Value: settings.SocialLinks}, {Key: "businessHours", Value: settings.BusinessHours},
		{Key: "maintenanceMode", Value: settings.MaintenanceMode}, {Key: "allowRegistration", Value: settings.AllowRegistration},
		{Key: "defaultCurrency", Value: settings.DefaultCurrency}, {Key: "timezone", Value: settings.Timezone},
	}
	if request.ChangedFields != nil {
		changed = bson.D{}
		for _, key := range []string{"siteName", "siteDescription", "contactEmail", "contactWhatsapp", "socialLinks", "businessHours", "maintenanceMode", "allowRegistration", "defaultCurrency", "timezone"} {
			if value, ok := request.ChangedFields[key]; ok {
				changed = append(changed, bson.E{Key: key, Value: value})
			}
		}
	}
	var targetID any
	if request.TargetID != nil {
		targetID = request.TargetID.Hex()
	}
	_, err := r.db.Collection("activity_log").InsertOne(c, bson.D{
		{Key: "_id", Value: bson.NewObjectID()}, {Key: "actor_admin_id", Value: actor},
		{Key: "actor_name_snapshot", Value: name}, {Key: "actor_email_snapshot", Value: email},
		{Key: "action_code", Value: "SETTINGS_UPDATED"}, {Key: "action_label", Value: label},
		{Key: "category", Value: "SETTINGS"}, {Key: "target_type", Value: "PLATFORM_SETTINGS"},
		{Key: "target_id", Value: targetID}, {Key: "target_reference", Value: "current"},
		{Key: "result", Value: "SUCCESS"}, {Key: "failure_code", Value: nil}, {Key: "reason", Value: nil},
		{Key: "changed_fields", Value: changed}, {Key: "request_id", Value: optionalValue(request.RequestID)},
		{Key: "source_ip", Value: optionalValue(request.SourceIP)}, {Key: "user_agent", Value: optionalValue(request.UserAgent)},
		{Key: "occurred_at", Value: settings.UpdatedAt},
	})
	return err
}

func optionalValue(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

var _ model.AdminReadRepository = (*AdminReadRepository)(nil)
