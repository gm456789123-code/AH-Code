import mysql from 'mysql2/promise';
import { DatabaseSync } from 'node:sqlite';
import path from 'path';
import { fileURLToPath } from 'url';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const SQLITE_PATH = path.join(__dirname, 'sent_code.db');

class UnifiedDatabase {
  constructor() {
    this.mode = 'sqlite';
    this.pool = null;
    this.sqlite = null;
  }

  async init() {
    const isMysqlConfigured = (process.env.DB_TYPE || 'mysql').toLowerCase() === 'mysql';

    if (isMysqlConfigured) {
      try {
        this.pool = mysql.createPool({
          host: process.env.DB_HOST || 'localhost',
          port: parseInt(process.env.DB_PORT || '3306', 10),
          user: process.env.DB_USER || 'sentcode',
          password: process.env.DB_PASSWORD || 'sentcodepassword',
          database: process.env.DB_NAME || 'sent_code',
          waitForConnections: true,
          connectionLimit: 10,
          queueLimit: 0,
        });

        // Test connection
        const conn = await this.pool.getConnection();
        await conn.ping();
        conn.release();

        this.mode = 'mysql';
        console.log(`🐬 Database connected: MySQL / MariaDB (Docker) at ${process.env.DB_HOST || 'localhost'}:${process.env.DB_PORT || 3306}`);
        console.log(`🌐 phpMyAdmin available at: http://localhost:8080 (User: root or ${process.env.DB_USER || 'sentcode'})`);

        await this.initMysqlTables();
        return;
      } catch (err) {
        console.warn(`⚠️ MySQL (Docker) not reachable yet (${err.message}). Falling back to SQLite temporary store.`);
        console.warn(`👉 To start MySQL + phpMyAdmin in Docker, run: cd web && docker compose up -d`);
      }
    }

    // SQLite Fallback
    this.mode = 'sqlite';
    this.sqlite = new DatabaseSync(SQLITE_PATH);
    this.initSqliteTables();
    console.log(`🗄️ Database connected: Local SQLite (sent_code.db)`);
  }

  async initMysqlTables() {
    await this.pool.execute(`
      CREATE TABLE IF NOT EXISTS users (
        id VARCHAR(64) PRIMARY KEY,
        email VARCHAR(191) UNIQUE NOT NULL,
        name VARCHAR(191),
        avatar VARCHAR(255),
        role VARCHAR(32) DEFAULT 'user',
        tokens INT DEFAULT 100,
        plan VARCHAR(32) DEFAULT 'free',
        session_token VARCHAR(128),
        created_at VARCHAR(64)
      ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
    `);

    await this.pool.execute(`
      CREATE TABLE IF NOT EXISTS settings (
        \`key\` VARCHAR(128) PRIMARY KEY,
        \`value\` TEXT
      ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
    `);

    await this.pool.execute(`
      CREATE TABLE IF NOT EXISTS training_data (
        id VARCHAR(64) PRIMARY KEY,
        user_id VARCHAR(64),
        type VARCHAR(64),
        prompt TEXT,
        response TEXT,
        feedback VARCHAR(64),
        tech_stack VARCHAR(128),
        created_at VARCHAR(64)
      ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
    `);

    // Insert default admin
    const [rows] = await this.pool.execute('SELECT id FROM users WHERE email = ?', ['admin@sent-code.dev']);
    if (rows.length === 0) {
      await this.pool.execute(`
        INSERT INTO users (id, email, name, role, tokens, plan, created_at)
        VALUES ('admin-001', 'admin@sent-code.dev', 'Admin', 'admin', 9999, 'unlimited', ?)
      `, [new Date().toISOString()]);
    }

    // Default settings
    const defaultSettings = [
      ['defaultFreeTokens', '100'],
      ['groqVisionModel', 'meta-llama/llama-4-scout-17b-16e-instruct'],
      ['groqVisionFallback', 'llama-3.2-11b-vision-preview'],
      ['maintenanceMode', 'false']
    ];

    for (const [k, v] of defaultSettings) {
      await this.pool.execute('INSERT IGNORE INTO settings (`key`, `value`) VALUES (?, ?)', [k, v]);
    }
  }

  initSqliteTables() {
    this.sqlite.exec(`
      PRAGMA journal_mode = WAL;
      PRAGMA synchronous = NORMAL;

      CREATE TABLE IF NOT EXISTS users (
        id TEXT PRIMARY KEY,
        email TEXT UNIQUE NOT NULL,
        name TEXT,
        avatar TEXT,
        role TEXT DEFAULT 'user',
        tokens INTEGER DEFAULT 100,
        plan TEXT DEFAULT 'free',
        session_token TEXT,
        created_at TEXT
      );

      CREATE TABLE IF NOT EXISTS settings (
        key TEXT PRIMARY KEY,
        value TEXT
      );

      CREATE TABLE IF NOT EXISTS training_data (
        id TEXT PRIMARY KEY,
        user_id TEXT,
        type TEXT,
        prompt TEXT,
        response TEXT,
        feedback TEXT,
        tech_stack TEXT,
        created_at TEXT
      );
    `);

    const adminCheck = this.sqlite.prepare("SELECT id FROM users WHERE email = 'admin@sent-code.dev'").get();
    if (!adminCheck) {
      this.sqlite.prepare(`
        INSERT INTO users (id, email, name, role, tokens, plan, created_at)
        VALUES ('admin-001', 'admin@sent-code.dev', 'Admin', 'admin', 9999, 'unlimited', ?)
      `).run(new Date().toISOString());
    }

    const defaultSettings = [
      ['defaultFreeTokens', '100'],
      ['groqVisionModel', 'meta-llama/llama-4-scout-17b-16e-instruct'],
      ['groqVisionFallback', 'llama-3.2-11b-vision-preview'],
      ['maintenanceMode', 'false']
    ];

    const insertSetting = this.sqlite.prepare('INSERT OR IGNORE INTO settings (key, value) VALUES (?, ?)');
    for (const [k, v] of defaultSettings) {
      insertSetting.run(k, v);
    }
  }

  // ─────────────────────────────────────────────
  // Unified User Methods
  // ─────────────────────────────────────────────
  async getUserByEmail(email) {
    if (this.mode === 'mysql') {
      const [rows] = await this.pool.execute('SELECT * FROM users WHERE LOWER(email) = LOWER(?)', [email]);
      return rows[0] || null;
    }
    return this.sqlite.prepare('SELECT * FROM users WHERE LOWER(email) = LOWER(?)').get(email) || null;
  }

  async getUserByToken(token) {
    if (!token) return null;
    if (this.mode === 'mysql') {
      const [rows] = await this.pool.execute('SELECT * FROM users WHERE session_token = ?', [token]);
      return rows[0] || null;
    }
    return this.sqlite.prepare('SELECT * FROM users WHERE session_token = ?').get(token) || null;
  }

  async getAllUsers() {
    if (this.mode === 'mysql') {
      const [rows] = await this.pool.execute('SELECT * FROM users ORDER BY created_at DESC');
      return rows;
    }
    return this.sqlite.prepare('SELECT * FROM users ORDER BY created_at DESC').all();
  }

  async createUser({ id, email, name, avatar, role, tokens, plan, sessionToken, createdAt }) {
    if (this.mode === 'mysql') {
      await this.pool.execute(`
        INSERT INTO users (id, email, name, avatar, role, tokens, plan, session_token, created_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
      `, [id, email.toLowerCase(), name, avatar || null, role || 'user', tokens, plan || 'free', sessionToken, createdAt]);
      return this.getUserByEmail(email);
    }

    this.sqlite.prepare(`
      INSERT INTO users (id, email, name, avatar, role, tokens, plan, session_token, created_at)
      VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
    `).run(id, email.toLowerCase(), name, avatar || null, role || 'user', tokens, plan || 'free', sessionToken, createdAt);
    return this.getUserByEmail(email);
  }

  async updateSessionToken(email, token) {
    if (this.mode === 'mysql') {
      await this.pool.execute('UPDATE users SET session_token = ? WHERE LOWER(email) = LOWER(?)', [token, email]);
      return;
    }
    this.sqlite.prepare('UPDATE users SET session_token = ? WHERE LOWER(email) = LOWER(?)').run(token, email);
  }

  async deductToken(userId) {
    if (this.mode === 'mysql') {
      await this.pool.execute('UPDATE users SET tokens = GREATEST(0, tokens - 1) WHERE id = ?', [userId]);
      return;
    }
    this.sqlite.prepare('UPDATE users SET tokens = MAX(0, tokens - 1) WHERE id = ?').run(userId);
  }

  async addTokens(email, amount) {
    if (this.mode === 'mysql') {
      await this.pool.execute('UPDATE users SET tokens = tokens + ? WHERE LOWER(email) = LOWER(?)', [amount, email]);
      return this.getUserByEmail(email);
    }
    this.sqlite.prepare('UPDATE users SET tokens = tokens + ? WHERE LOWER(email) = LOWER(?)').run(amount, email);
    return this.getUserByEmail(email);
  }

  // ─────────────────────────────────────────────
  // Settings
  // ─────────────────────────────────────────────
  async getSetting(key) {
    if (this.mode === 'mysql') {
      const [rows] = await this.pool.execute('SELECT `value` FROM settings WHERE `key` = ?', [key]);
      return rows[0] ? rows[0].value : null;
    }
    const row = this.sqlite.prepare('SELECT value FROM settings WHERE key = ?').get(key);
    return row ? row.value : null;
  }

  async setSetting(key, value) {
    if (this.mode === 'mysql') {
      await this.pool.execute('INSERT INTO settings (`key`, `value`) VALUES (?, ?) ON DUPLICATE KEY UPDATE `value` = ?', [key, value, value]);
      return;
    }
    this.sqlite.prepare('INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)').run(key, value);
  }

  // ─────────────────────────────────────────────
  // Training Data
  // ─────────────────────────────────────────────
  async addTrainingData({ id, userId, type, prompt, response, feedback, techStack, createdAt }) {
    if (this.mode === 'mysql') {
      await this.pool.execute(`
        INSERT INTO training_data (id, user_id, type, prompt, response, feedback, tech_stack, created_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?)
      `, [id, userId, type, prompt, response || null, feedback || null, techStack || null, createdAt]);
      return;
    }

    this.sqlite.prepare(`
      INSERT INTO training_data (id, user_id, type, prompt, response, feedback, tech_stack, created_at)
      VALUES (?, ?, ?, ?, ?, ?, ?, ?)
    `).run(id, userId, type, prompt, response || null, feedback || null, techStack || null, createdAt);
  }

  async getTrainingDataCount() {
    if (this.mode === 'mysql') {
      const [rows] = await this.pool.execute('SELECT COUNT(*) as cnt FROM training_data');
      return rows[0].cnt;
    }
    const row = this.sqlite.prepare('SELECT COUNT(*) as cnt FROM training_data').get();
    return row ? row.cnt : 0;
  }
}

export const db = new UnifiedDatabase();
