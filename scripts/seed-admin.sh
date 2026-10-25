#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
project_root=$(CDPATH= cd -- "$script_dir/.." && pwd)
cd "$project_root"

usage() {
  printf '%s\n' 'Usage: sh scripts/seed-admin.sh [--permissions-only]' >&2
  exit 2
}

permissions_only=false
case $# in
  0) ;;
  1)
    [ "$1" = '--permissions-only' ] || usage
    permissions_only=true
    ;;
  *) usage ;;
esac

trim() {
  value=$1
  value=${value#"${value%%[![:space:]]*}"}
  value=${value%"${value##*[![:space:]]}"}
  printf '%s' "$value"
}

load_local_env() {
  env_file=$project_root/.env
  [ -f "$env_file" ] || return 0

  while IFS= read -r line || [ -n "$line" ]; do
    line=$(trim "$line")
    case $line in
      ''|'#'*) continue ;;
      export\ *) line=${line#export } ;;
    esac
    case $line in *=*) ;; *) continue ;; esac

    key=$(trim "${line%%=*}")
    value=$(trim "${line#*=}")
    case $value in
      \"*\") value=${value#\"}; value=${value%\"} ;;
      \'*\') value=${value#\'}; value=${value%\'} ;;
    esac

    case $key in
      MONGODB_URL)
        if [ "${MONGODB_URL+x}" != x ]; then MONGODB_URL=$value; export MONGODB_URL; fi
        ;;
      MONGODB_URI)
        if [ "${MONGODB_URI+x}" != x ]; then MONGODB_URI=$value; export MONGODB_URI; fi
        ;;
      MONGODB_DATABASE)
        if [ "${MONGODB_DATABASE+x}" != x ]; then MONGODB_DATABASE=$value; export MONGODB_DATABASE; fi
        ;;
      ADMIN_ACCOUNT_ID)
        if [ "${ADMIN_ACCOUNT_ID+x}" != x ]; then ADMIN_ACCOUNT_ID=$value; export ADMIN_ACCOUNT_ID; fi
        ;;
      ADMIN_USERNAME)
        if [ "${ADMIN_USERNAME+x}" != x ]; then ADMIN_USERNAME=$value; export ADMIN_USERNAME; fi
        ;;
      ADMIN_NAME)
        if [ "${ADMIN_NAME+x}" != x ]; then ADMIN_NAME=$value; export ADMIN_NAME; fi
        ;;
      ADMIN_EMAIL)
        if [ "${ADMIN_EMAIL+x}" != x ]; then ADMIN_EMAIL=$value; export ADMIN_EMAIL; fi
        ;;
      ADMIN_PASSWORD_HASH)
        if [ "${ADMIN_PASSWORD_HASH+x}" != x ]; then ADMIN_PASSWORD_HASH=$value; export ADMIN_PASSWORD_HASH; fi
        ;;
    esac
  done < "$env_file"
}

load_local_env

if [ -z "${MONGODB_URL:-}" ] && [ -n "${MONGODB_URI:-}" ]; then
  MONGODB_URL=$MONGODB_URI
  export MONGODB_URL
fi
if [ -z "${MONGODB_URL:-}" ]; then
  printf '%s\n' 'MONGODB_URL is required.' >&2
  exit 1
fi
if ! command -v mongosh >/dev/null 2>&1; then
  printf '%s\n' 'mongosh is required. Install MongoDB Shell and add it to PATH.' >&2
  exit 1
fi

if [ "$permissions_only" != true ]; then
  for variable in ADMIN_ACCOUNT_ID ADMIN_USERNAME ADMIN_NAME ADMIN_EMAIL ADMIN_PASSWORD_HASH; do
    eval "value=\${$variable-}"
    if [ -z "$value" ]; then
      printf '%s\n' "$variable is required for a full Super Admin seed." >&2
      exit 1
    fi
  done
fi

export SEED_PERMISSIONS_ONLY=$permissions_only

# The connection URI and credentials are read from process.env inside mongosh,
# so they are not added to mongosh's command-line arguments or printed.
mongosh_script=$(cat <<'MONGOSH'
(() => {
  const safeError = (message) => {
    const error = new Error(message);
    error.isSafeSeedError = true;
    throw error;
  };
  const env = process.env;
  const uri = env.MONGODB_URL || env.MONGODB_URI;
  if (!uri) safeError('MONGODB_URL is required.');

  let databaseName = (env.MONGODB_DATABASE || '').trim();
  if (!databaseName) {
    const match = uri.match(/^mongodb(?:\+srv)?:\/\/[^/?]+\/([^?]*)/i);
    databaseName = match && match[1] ? decodeURIComponent(match[1]) : '';
  }
  if (!databaseName) safeError('MONGODB_DATABASE is required when the URI has no database path.');

  const permissionsOnly = env.SEED_PERMISSIONS_ONLY === 'true';
  const accountId = (env.ADMIN_ACCOUNT_ID || '').trim();
  const username = (env.ADMIN_USERNAME || '').trim();
  const name = (env.ADMIN_NAME || '').trim();
  const email = (env.ADMIN_EMAIL || '').trim();
  const passwordHash = env.ADMIN_PASSWORD_HASH || '';
  const roleId = 'role_super_admin';
  const now = new Date();
  const permissions = [
    { id: 'permission_role_read', key: 'role.read', name: 'Read roles', description: 'View administrator roles and their permissions.' },
    { id: 'permission_role_create', key: 'role.create', name: 'Create roles', description: 'Create custom administrator roles.' },
    { id: 'permission_role_update', key: 'role.update', name: 'Update roles', description: 'Update custom administrator roles and permissions.' },
    { id: 'permission_role_delete', key: 'role.delete', name: 'Delete roles', description: 'Soft-delete custom administrator roles.' },
    { id: 'permission_admin_account_read', key: 'admin-account.read', name: 'Read administrator accounts', description: 'View administrator accounts and their assigned roles.' },
    { id: 'permission_game_read', key: 'game.read', name: 'Read games', description: 'View the admin game catalog.' },
    { id: 'permission_game_create', key: 'game.create', name: 'Create games', description: 'Create games in the admin catalog.' },
    { id: 'permission_game_update', key: 'game.update', name: 'Update games', description: 'Update games in the admin catalog.' },
    { id: 'permission_game_delete', key: 'game.delete', name: 'Delete games', description: 'Soft-delete games from the admin catalog.' },
  ];

  const validateHash = (hash) => {
    const parts = hash.split('$');
    if (parts.length !== 6 || parts[1] !== 'argon2id' || parts[2] !== 'v=19' || parts[3] !== 'm=65536,t=3,p=2') {
      safeError('ADMIN_PASSWORD_HASH must be an Argon2id v19 hash using m=65536,t=3,p=2.');
    }
    if (!/^[A-Za-z0-9+/]+$/.test(parts[4]) || !/^[A-Za-z0-9+/]+$/.test(parts[5])) {
      safeError('ADMIN_PASSWORD_HASH has invalid base64 salt or digest.');
    }
    const salt = Buffer.from(parts[4], 'base64');
    const digest = Buffer.from(parts[5], 'base64');
    const encodedSalt = salt.toString('base64').replace(/=+$/, '');
    const encodedDigest = digest.toString('base64').replace(/=+$/, '');
    if (encodedSalt !== parts[4] || encodedDigest !== parts[5] || salt.length < 8 || salt.length > 64 || digest.length < 16 || digest.length > 64 || hash.length > 512) {
      safeError('ADMIN_PASSWORD_HASH has invalid salt or digest length.');
    }
  };

  if (!permissionsOnly) {
    if (!/^admin_[A-Za-z0-9_-]{1,58}$/.test(accountId)) safeError('ADMIN_ACCOUNT_ID is invalid.');
    if (!username || username.length > 30 || username.includes('@')) safeError('ADMIN_USERNAME is invalid.');
    if (!name || name.length > 150 || !email || email.length > 254) safeError('ADMIN_NAME or ADMIN_EMAIL is invalid.');
    if (!passwordHash) safeError('ADMIN_PASSWORD_HASH is required.');
    validateHash(passwordHash);
  }

  let database;
  try {
    database = connect(uri).getSiblingDB(databaseName);
  } catch (_) {
    safeError('Unable to connect to MongoDB; verify the URI and network access.');
  }

  const createIndexes = () => {
    const col = (collection) => database.getCollection(collection);
    col('admin_account').createIndex({ admin_account_id: 1 }, { name: 'uq_admin_account_id', unique: true });
    col('admin_account').createIndex({ username: 1 }, { name: 'uq_admin_account_username_ci', unique: true, collation: { locale: 'en', strength: 2 } });
    col('admin_account').createIndex({ email: 1 }, { name: 'uq_admin_account_email_ci', unique: true, collation: { locale: 'en', strength: 2 } });
    col('admin_account').createIndex({ status: 1, username: 1, admin_account_id: 1 }, { name: 'ix_admin_account_list', partialFilterExpression: { deleted_at: null } });
    col('admin_credential').createIndex({ credential_id: 1 }, { name: 'uq_admin_credential_id', unique: true });
    col('admin_credential').createIndex({ admin_account_id: 1 }, { name: 'uq_admin_credential_account', unique: true });
    col('admin_session').createIndex({ session_id: 1 }, { name: 'uq_admin_session_id', unique: true });
    col('admin_session').createIndex({ refresh_token_digest: 1 }, { name: 'uq_admin_session_refresh_digest', unique: true });
    col('admin_session').createIndex({ family_id: 1, revoked_at: 1, rotated_at: 1 }, { name: 'ix_admin_session_family_active' });
    col('admin_session').createIndex({ admin_account_id: 1, revoked_at: 1, expires_at: 1 }, { name: 'ix_admin_session_account_active' });
    col('admin_session').createIndex({ expires_at: 1 }, { name: 'ttl_admin_session_expiry', expireAfterSeconds: 0 });
    col('account_role').createIndex({ account_role_id: 1 }, { name: 'uq_account_role_id', unique: true });
    col('account_role').createIndex({ admin_account_id: 1 }, { name: 'uq_account_role_admin_active', unique: true, partialFilterExpression: { revoked_at: null } });
    col('account_role').createIndex({ role_id: 1, admin_account_id: 1 }, { name: 'ix_account_role_role_active', partialFilterExpression: { revoked_at: null } });
    col('admin_role').createIndex({ role_id: 1 }, { name: 'uq_admin_role_id', unique: true });
    col('admin_role').createIndex({ name: 1 }, { name: 'uq_admin_role_name_active', unique: true, collation: { locale: 'en', strength: 2 }, partialFilterExpression: { deleted_at: null } });
    col('admin_role').createIndex({ code: 1 }, { name: 'uq_admin_role_code_active', unique: true, partialFilterExpression: { deleted_at: null } });
    col('admin_role').createIndex({ status: 1, name: 1, role_id: 1 }, { name: 'ix_admin_role_list', partialFilterExpression: { deleted_at: null } });
    col('role_permission').createIndex({ role_id: 1, permission_id: 1 }, { name: 'uq_role_permission_pair', unique: true });
    col('permission').createIndex({ permission_id: 1 }, { name: 'uq_permission_id', unique: true });
    col('permission').createIndex({ permission_key: 1 }, { name: 'uq_permission_key', unique: true });
    col('permission').createIndex({ is_active: 1, permission_key: 1 }, { name: 'ix_permission_active_key' });
    col('rate_limit_windows').createIndex({ expires_at: 1 }, { name: 'ttl_rate_limit_expiry', expireAfterSeconds: 0 });
  };

  try {
    createIndexes();
  } catch (_) {
    safeError('MongoDB index setup failed. Check for duplicate or incomplete legacy records.');
  }

  const mongo = database.getMongo();
  const session = mongo.startSession();
  const tx = session.getDatabase(databaseName);
  try {
    session.startTransaction();
    const permissionCollection = tx.getCollection('permission');
    for (const permission of permissions) {
      const existing = permissionCollection.findOne({ $or: [
        { permission_id: permission.id }, { permission_key: permission.key },
      ] });
      if (!existing) {
        permissionCollection.insertOne({
          permission_id: permission.id, permission_key: permission.key, name: permission.name,
          description: permission.description, is_sensitive: false, is_active: true,
          created_at: now, updated_at: now,
        });
      } else {
        if (existing.permission_id !== permission.id || existing.permission_key !== permission.key) {
          safeError('An existing permission conflicts with the immutable system catalog.');
        }
        permissionCollection.updateOne({ permission_id: permission.id }, { $set: {
          name: permission.name, description: permission.description, is_sensitive: false,
          is_active: true, updated_at: now,
        } });
      }
    }

    const roles = tx.getCollection('admin_role');
    const activeSuperAdmins = roles.countDocuments({ code: 'SUPER_ADMIN', status: 'ACTIVE', deleted_at: null });
    if (activeSuperAdmins > 1) safeError('More than one active SUPER_ADMIN role exists.');
    let role = roles.findOne({ $or: [{ role_id: roleId }, { code: 'SUPER_ADMIN' }] });
    if (role && (role.role_id !== roleId || role.code !== 'SUPER_ADMIN' || role.name !== 'Super Admin' || role.status !== 'ACTIVE' || role.is_system !== true || role.deleted_at != null)) {
      safeError('Existing SUPER_ADMIN role differs from the immutable bootstrap definition.');
    }
    if (!role && permissionsOnly) safeError('The existing immutable SUPER_ADMIN role was not found.');
    if (!role) {
      roles.insertOne({
        role_id: roleId, name: 'Super Admin', code: 'SUPER_ADMIN', status: 'ACTIVE',
        is_system: true, description: 'System administrator role', created_at: now,
        updated_at: now, deleted_at: null,
      });
    }

    const grants = tx.getCollection('role_permission');
    for (const permission of permissions) {
      const grant = grants.findOne({ role_id: roleId, permission_id: permission.id });
      if (!grant) grants.insertOne({ role_id: roleId, permission_id: permission.id, created_at: now });
    }

    if (!permissionsOnly) {
      const accounts = tx.getCollection('admin_account');
      const existingAccount = accounts.findOne({ admin_account_id: accountId });
      if (existingAccount) {
        if (existingAccount.username !== username || existingAccount.email !== email || existingAccount.name !== name || existingAccount.status !== 'ACTIVE' || existingAccount.deleted_at != null) {
          safeError('Existing admin account differs from the supplied bootstrap values.');
        }
      } else {
        accounts.insertOne({ admin_account_id: accountId, username, name, email, status: 'ACTIVE', last_active: null, created_at: now, updated_at: now, deleted_at: null });
      }

      const credentials = tx.getCollection('admin_credential');
      const existingCredential = credentials.findOne({ admin_account_id: accountId });
      if (!existingCredential) {
        credentials.insertOne({ credential_id: `credential_${accountId}`, admin_account_id: accountId, password_hash: passwordHash, password_updated_at: now, created_at: now, updated_at: now });
      }

      const assignments = tx.getCollection('account_role');
      const activeAssignments = assignments.find({ admin_account_id: accountId, revoked_at: null }).toArray();
      if (activeAssignments.length > 1 || (activeAssignments.length === 1 && activeAssignments[0].role_id !== roleId)) {
        safeError('Admin account has a conflicting active role assignment.');
      }
      if (activeAssignments.length === 0) {
        assignments.insertOne({ account_role_id: `account_role_${accountId}`, admin_account_id: accountId, role_id: roleId, assigned_at: now, revoked_at: null });
      }
    }

    session.commitTransaction();
    print(permissionsOnly ? 'Seeded system permission catalog and Super Admin grants.' : 'Seeded Super Admin account and system permission grants.');
  } catch (error) {
    try { session.abortTransaction(); } catch (_) {}
    if (error && error.isSafeSeedError) {
      print(`Seeder failed: ${error.message}`);
    } else {
      print('Seeder failed. Check MongoDB connectivity, index conflicts, and existing records. Details are suppressed to protect credentials.');
    }
    quit(1);
  } finally {
    session.endSession();
  }
})();
MONGOSH
)

mongosh --nodb --norc --quiet --eval "$mongosh_script"
