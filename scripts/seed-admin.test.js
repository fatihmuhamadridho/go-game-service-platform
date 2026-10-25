const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

const script = fs.readFileSync(path.join(__dirname, "seed-admin.js"), "utf8");
const validHash = "$argon2id$v=19$m=65536,t=3,p=2$c2FsdC1zYWx0LXNpemU$YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWE";

class FakeDatabase {
  constructor() {
    this.collections = new Map();
    this.indexes = [];
    this.transactions = { commits: 0, aborts: 0 };
    this.objectId = 0;
  }

  getCollection(name) {
    if (!this.collections.has(name)) this.collections.set(name, []);
    const database = this;
    return {
      createIndex(keys, options) { database.indexes.push({ name, keys, options }); },
      countDocuments(query) { return docs().filter((document) => matches(document, query)).length; },
      findOne(query) { return docs().find((document) => matches(document, query)) || null; },
      find(query) { return { toArray: () => docs().filter((document) => matches(document, query)) }; },
      insertOne(document) { docs().push(structuredClone(document)); },
    };
    function docs() { return database.collections.get(name); }
  }

  getMongo() {
    return this;
  }

  startSession() {
    const database = this;
    const snapshot = structuredClone(Object.fromEntries(this.collections));
    return {
      startTransaction() {},
      getDatabase() { return database; },
      commitTransaction() { database.transactions.commits++; },
      abortTransaction() {
        database.transactions.aborts++;
        database.collections = new Map(Object.entries(snapshot));
      },
      endSession() {},
    };
  }
}

class FakeMongo {
  constructor() { this.database = FakeMongo.database; }
  getDB() { return this.database; }
}

function matches(document, query) {
  if (query.$or) return query.$or.some((condition) => matches(document, condition));
  return Object.entries(query).every(([key, value]) => {
    if (value === null) return document[key] == null;
    return document[key] === value;
  });
}

function runSeed(environment, database = new FakeDatabase()) {
  FakeMongo.database = database;
  const output = [];
  vm.runInNewContext(script, {
    process: { env: environment },
    URL,
    Mongo: FakeMongo,
    ObjectId: class { toHexString() { return String(++database.objectId).padStart(24, "0"); } },
    print: (value) => output.push(value),
  }, { filename: "seed-admin.js" });
  return { database, output };
}

function environment(overrides = {}) {
  return {
    MONGODB_URL: "mongodb://localhost:27017/game_service_platform?replicaSet=rs0",
    ADMIN_ACCOUNT_ID: "admin_initial",
    ADMIN_USERNAME: "root.admin",
    ADMIN_NAME: "Initial Admin",
    ADMIN_EMAIL: "root@example.test",
    ADMIN_PASSWORD_HASH: validHash,
    ...overrides,
  };
}

function run() {
  const seeded = runSeed(environment());
  const roles = seeded.database.getCollection("admin_role").find({}).toArray();
  const accounts = seeded.database.getCollection("admin_account").find({}).toArray();
  const credentials = seeded.database.getCollection("admin_credential").find({}).toArray();
  const assignments = seeded.database.getCollection("account_role").find({}).toArray();
  assert.equal(roles.length, 1);
  assert.equal(roles[0].code, "SUPER_ADMIN");
  assert.equal(roles[0].is_system, true);
  assert.equal(accounts.length, 1);
  assert.equal(credentials.length, 1);
  assert.equal(credentials[0].password_hash, validHash);
  assert.equal(assignments.length, 1);
  assert.equal(seeded.database.transactions.commits, 1);

  const repeated = runSeed(environment({ ADMIN_PASSWORD_HASH: validHash.replace("YWFh", "YmJi") }), seeded.database);
  assert.equal(repeated.database.getCollection("admin_role").find({}).toArray().length, 1);
  assert.equal(repeated.database.getCollection("admin_account").find({}).toArray().length, 1);
  assert.equal(repeated.database.getCollection("admin_credential").find({}).toArray().length, 1);
  assert.equal(repeated.database.getCollection("admin_credential").find({}).toArray()[0].password_hash, validHash);
  assert.equal(repeated.database.getCollection("account_role").find({}).toArray().length, 1);

  assert.throws(() => runSeed(environment({ ADMIN_PASSWORD_HASH: "plaintext" })), /encoded Argon2id hash/);
  assert.throws(() => runSeed(environment({ ADMIN_ACCOUNT_ID: "" })), /Missing required environment variable/);

  const conflictDatabase = new FakeDatabase();
  conflictDatabase.getCollection("admin_role").insertOne({ role_id: "role_super_admin", name: "Changed", code: "SUPER_ADMIN", status: "ACTIVE", is_system: true, deleted_at: null });
  assert.throws(() => runSeed(environment(), conflictDatabase), /immutable bootstrap definition/);
  assert.equal(conflictDatabase.getCollection("admin_account").find({}).toArray().length, 0);

  process.stdout.write("seed-admin: bootstrap, idempotency, invalid hash, and immutable-role checks passed\n");
}

run();
