// Run with mongosh after setting bootstrap values through a secret manager.
// This script never accepts or stores a raw password.
const required = ["MONGODB_URL", "ADMIN_ACCOUNT_ID", "ADMIN_USERNAME", "ADMIN_NAME", "ADMIN_EMAIL", "ADMIN_PASSWORD_HASH"];
for (const key of required) {
  if (!process.env[key]) throw new Error(`Missing required environment variable: ${key}`);
}

const uri = process.env.MONGODB_URL;
const uriPath = new URL(uri).pathname.replace(/^\/+|\/+$/g, "");
const databaseName = process.env.MONGODB_DATABASE || uriPath;
if (!databaseName) throw new Error("MONGODB_DATABASE is required when MONGODB_URL has no database path");
const accountId = process.env.ADMIN_ACCOUNT_ID.trim();
const username = process.env.ADMIN_USERNAME.trim();
const displayName = process.env.ADMIN_NAME.trim();
const email = process.env.ADMIN_EMAIL.trim();
const passwordHash = process.env.ADMIN_PASSWORD_HASH;
if (!/^admin_[A-Za-z0-9_-]{1,58}$/.test(accountId)) throw new Error("ADMIN_ACCOUNT_ID must use admin_ prefix");
if (!username || username.length > 30 || username.includes("@")) throw new Error("ADMIN_USERNAME is invalid");
if (!displayName || displayName.length > 150 || !email || email.length > 254) throw new Error("Admin name/email is invalid");
if (passwordHash.length > 512 || !/^\$argon2id\$v=19\$m=65536,t=3,p=2\$[A-Za-z0-9+/]+\$[A-Za-z0-9+/]+$/.test(passwordHash)) {
  throw new Error("ADMIN_PASSWORD_HASH must be an encoded Argon2id hash with the configured service parameters");
}

const adminDatabase = new Mongo(uri).getDB(databaseName);
const roleId = "role_super_admin";
const now = new Date();
adminDatabase.getCollection("admin_account").createIndex({ admin_account_id: 1 }, { unique: true, name: "uq_admin_account_id" });
adminDatabase.getCollection("admin_account").createIndex({ username: 1 }, { unique: true, name: "uq_admin_account_username_ci", collation: { locale: "en", strength: 2 } });
adminDatabase.getCollection("admin_account").createIndex({ email: 1 }, { unique: true, name: "uq_admin_account_email_ci", collation: { locale: "en", strength: 2 } });
adminDatabase.getCollection("admin_credential").createIndex({ admin_account_id: 1 }, { unique: true, name: "uq_admin_credential_account" });
adminDatabase.getCollection("admin_role").createIndex({ role_id: 1 }, { unique: true, name: "uq_admin_role_id" });
adminDatabase.getCollection("admin_role").createIndex({ code: 1 }, { unique: true, name: "uq_admin_role_code_active", partialFilterExpression: { deleted_at: null } });
adminDatabase.getCollection("account_role").createIndex({ account_role_id: 1 }, { unique: true, name: "uq_account_role_id" });
adminDatabase.getCollection("account_role").createIndex({ admin_account_id: 1 }, { unique: true, name: "uq_account_role_admin_active", partialFilterExpression: { revoked_at: null } });
const session = adminDatabase.getMongo().startSession();
try {
  session.startTransaction();
  const db = session.getDatabase(databaseName);
  const roles = db.getCollection("admin_role");
  const accounts = db.getCollection("admin_account");
  const credentials = db.getCollection("admin_credential");
  const assignments = db.getCollection("account_role");

  const existingSuperAdmins = roles.countDocuments({ code: "SUPER_ADMIN", status: "ACTIVE", deleted_at: null });
  if (existingSuperAdmins > 1) throw new Error("More than one active SUPER_ADMIN role exists");
  const existingRole = roles.findOne({ $or: [{ role_id: roleId }, { code: "SUPER_ADMIN" }] });
  if (existingRole) {
    if (existingRole.role_id !== roleId || existingRole.code !== "SUPER_ADMIN" || existingRole.name !== "Super Admin" ||
        existingRole.status !== "ACTIVE" || existingRole.is_system !== true || existingRole.deleted_at != null) {
      throw new Error("Existing SUPER_ADMIN role differs from immutable bootstrap definition");
    }
  } else {
    roles.insertOne({ role_id: roleId, name: "Super Admin", code: "SUPER_ADMIN", status: "ACTIVE", is_system: true,
      description: "System administrator role", created_at: now, updated_at: now, deleted_at: null });
  }

  const existingAccount = accounts.findOne({ admin_account_id: accountId });
  if (existingAccount) {
    if (existingAccount.username !== username || existingAccount.email !== email || existingAccount.name !== displayName ||
        existingAccount.status !== "ACTIVE" || existingAccount.deleted_at != null) {
      throw new Error("Existing admin account differs from bootstrap values");
    }
  } else {
    accounts.insertOne({ admin_account_id: accountId, username, name: displayName, email, status: "ACTIVE",
      last_active: null, created_at: now, updated_at: now, deleted_at: null });
  }

  const existingCredential = credentials.findOne({ admin_account_id: accountId });
  if (!existingCredential) {
    credentials.insertOne({ credential_id: `credential_${new ObjectId().toHexString()}`, admin_account_id: accountId,
      password_hash: passwordHash, password_updated_at: now, created_at: now, updated_at: now });
  }

  const activeAssignments = assignments.find({ admin_account_id: accountId, revoked_at: null }).toArray();
  if (activeAssignments.length > 1 || (activeAssignments.length === 1 && activeAssignments[0].role_id !== roleId)) {
    throw new Error("Admin account has conflicting active role assignment");
  }
  if (activeAssignments.length === 0) {
    assignments.insertOne({ account_role_id: `account_role_${new ObjectId().toHexString()}`, admin_account_id: accountId,
      role_id: roleId, assigned_at: now, revoked_at: null });
  }

  session.commitTransaction();
  print(`Seeded immutable SUPER_ADMIN account ${accountId}`);
} catch (error) {
  session.abortTransaction();
  throw error;
} finally {
  session.endSession();
}
