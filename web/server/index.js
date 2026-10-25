import express from 'express';
import cors from 'cors';
import path from 'path';
import fs from 'fs';
import { fileURLToPath } from 'url';
import crypto from 'crypto';
import { db } from './db.js';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

const app = express();
const PORT = process.env.PORT || 3000;

// ─────────────────────────────────────────────
// Middleware
// ─────────────────────────────────────────────
app.use(cors({ origin: '*' }));
app.use(express.json({ limit: '25mb' }));
app.use(express.urlencoded({ extended: true, limit: '25mb' }));

// ─────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────
function generateToken(email) {
  return crypto.createHmac('sha256', process.env.SECRET || 'sent-code-secret-2026')
    .update(email + Date.now()).digest('hex').slice(0, 32);
}

function avatarInitials(name, email) {
  const src = name || email || '?';
  return src.slice(0, 2).toUpperCase();
}

// ─────────────────────────────────────────────
// API: Health & Status
// ─────────────────────────────────────────────
app.get('/api/health', async (req, res) => {
  const groqModel = (await db.getSetting('groqVisionModel')) || 'meta-llama/llama-4-scout-17b-16e-instruct';
  res.json({
    status: 'ok',
    service: 'Sent-Code Web Gateway',
    database: db.mode === 'mysql' ? 'MySQL / MariaDB (Docker)' : 'SQLite (WAL Mode)',
    phpmyadmin: 'http://localhost:8080',
    version: '1.0.0',
    timestamp: new Date().toISOString(),
    groqModel
  });
});

// ─────────────────────────────────────────────
// API: Auth
// ─────────────────────────────────────────────

// POST /api/auth/login  - Login or auto-register
app.post('/api/auth/login', async (req, res) => {
  const { email } = req.body;
  if (!email || !email.includes('@')) {
    return res.status(400).json({ error: 'Valid email is required' });
  }

  let user = await db.getUserByEmail(email);

  if (!user) {
    const sessionToken = generateToken(email);
    const defaultTokens = parseInt((await db.getSetting('defaultFreeTokens')) || '100', 10);
    user = await db.createUser({
      id: `user-${Date.now()}`,
      email: email.toLowerCase(),
      name: email.split('@')[0],
      avatar: null,
      role: 'user',
      tokens: defaultTokens,
      plan: 'free',
      sessionToken,
      createdAt: new Date().toISOString()
    });
  } else {
    if (!user.session_token) {
      const sessionToken = generateToken(email);
      await db.updateSessionToken(email, sessionToken);
      user.session_token = sessionToken;
    }
  }

  res.json({
    success: true,
    user: {
      id: user.id,
      email: user.email,
      name: user.name,
      avatar: user.avatar,
      initials: avatarInitials(user.name, user.email),
      role: user.role,
      tokens: user.tokens,
      plan: user.plan,
      sessionToken: user.session_token
    }
  });
});

// GET /api/auth/me?token=xxx  - Get current user profile
app.get('/api/auth/me', async (req, res) => {
  const { token } = req.query;
  if (!token) return res.status(401).json({ error: 'Token required' });

  const user = await db.getUserByToken(token);
  if (!user) return res.status(404).json({ error: 'User not found' });

  res.json({
    id: user.id,
    email: user.email,
    name: user.name,
    avatar: user.avatar,
    initials: avatarInitials(user.name, user.email),
    role: user.role,
    tokens: user.tokens,
    plan: user.plan
  });
});

// ─────────────────────────────────────────────
// API: Token Quota (Gatekeeper)
// ─────────────────────────────────────────────

// POST /api/tokens/verify  - Check & deduct quota
app.post('/api/tokens/verify', async (req, res) => {
  const { token, deduct } = req.body;
  if (!token) return res.status(400).json({ error: 'Session token required' });

  const user = await db.getUserByToken(token);
  if (!user) return res.status(404).json({ error: 'User not found' });

  if (user.plan === 'unlimited' || user.role === 'admin') {
    return res.json({ allowed: true, tokens: user.tokens, unlimited: true });
  }

  if (user.tokens <= 0) {
    return res.status(403).json({
      allowed: false,
      tokens: 0,
      message: "You've reached your usage limit. Please refill tokens to continue."
    });
  }

  if (deduct) {
    await db.deductToken(user.id);
  }

  res.json({ allowed: true, tokens: deduct ? Math.max(0, user.tokens - 1) : user.tokens });
});

// POST /api/tokens/refill  - Admin refill tokens
app.post('/api/tokens/refill', async (req, res) => {
  const { adminToken, targetEmail, amount } = req.body;
  const admin = await db.getUserByToken(adminToken);
  if (!admin || admin.role !== 'admin') return res.status(403).json({ error: 'Admin only' });

  const updated = await db.addTokens(targetEmail, parseInt(amount, 10) || 100);
  if (!updated) return res.status(404).json({ error: 'Target user not found' });

  res.json({ success: true, newTokens: updated.tokens });
});

// ─────────────────────────────────────────────
// API: Vision Gateway (Groq & Gemini Multimodal)
// ─────────────────────────────────────────────

// GET /api/vision/config - Get current vision provider status
app.get('/api/vision/config', async (req, res) => {
  const groqKey = (await db.getSetting('groqApiKey')) || process.env.GROQ_API_KEY || '';
  const geminiKey = (await db.getSetting('geminiApiKey')) || process.env.GEMINI_API_KEY || '';
  const visionModel = (await db.getSetting('groqVisionModel')) || 'llama-3.2-11b-vision-preview';

  res.json({
    hasGroqKey: !!groqKey,
    hasGeminiKey: !!geminiKey,
    groqApiKey: groqKey ? `${groqKey.slice(0, 6)}...${groqKey.slice(-4)}` : '',
    geminiApiKey: geminiKey ? `${geminiKey.slice(0, 6)}...${geminiKey.slice(-4)}` : '',
    visionModel,
    activeProvider: groqKey ? 'groq' : geminiKey ? 'gemini' : 'none'
  });
});

// POST /api/vision/config - Save vision API keys
app.post('/api/vision/config', async (req, res) => {
  const { groqApiKey, geminiApiKey, visionModel } = req.body;
  if (groqApiKey !== undefined) {
    await db.setSetting('groqApiKey', groqApiKey.trim());
  }
  if (geminiApiKey !== undefined) {
    await db.setSetting('geminiApiKey', geminiApiKey.trim());
  }
  if (visionModel) {
    await db.setSetting('groqVisionModel', visionModel.trim());
  }
  res.json({ success: true, message: 'Vision settings updated successfully' });
});

app.post('/api/vision/analyze', async (req, res) => {
  const { imageBase64, prompt, token } = req.body;

  if (!imageBase64) {
    return res.status(400).json({ error: 'imageBase64 is required' });
  }

  if (token) {
    const user = await db.getUserByToken(token);
    if (!user || (user.tokens <= 0 && user.plan !== 'unlimited')) {
      return res.status(403).json({ error: 'Insufficient tokens for vision analysis' });
    }
  }

  const groqApiKey = (await db.getSetting('groqApiKey')) || process.env.GROQ_API_KEY || '';
  const geminiApiKey = (await db.getSetting('geminiApiKey')) || process.env.GEMINI_API_KEY || '';
  const model = (await db.getSetting('groqVisionModel')) || 'llama-3.2-11b-vision-preview';

  // If neither key is configured, return error so client falls back to local vision models
  if (!groqApiKey && !geminiApiKey) {
    return res.status(400).json({
      success: false,
      error: 'NO_VISION_KEY',
      message: 'No cloud vision key configured. Please add a Groq API key in Settings or use local vision models.'
    });
  }

  // 1. Try Groq Vision first if key is present
  if (groqApiKey) {
    try {
      const imageUrl = imageBase64.startsWith('data:')
        ? imageBase64
        : `data:image/jpeg;base64,${imageBase64}`;

      const response = await fetch('https://api.groq.com/openai/v1/chat/completions', {
        method: 'POST',
        headers: {
          'Authorization': `Bearer ${groqApiKey}`,
          'Content-Type': 'application/json'
        },
        body: JSON.stringify({
          model,
          messages: [{
            role: 'user',
            content: [
              {
                type: 'text',
                text: prompt || 'Analyze this image in detail. Describe: UI components, layout, colors, text, code structure, or any errors visible. Provide a structured technical specification a developer can implement directly.'
              },
              { type: 'image_url', image_url: { url: imageUrl } }
            ]
          }],
          max_tokens: 2048,
          temperature: 0.2
        })
      });

      if (response.ok) {
        const data = await response.json();
        const spec = data.choices?.[0]?.message?.content;
        if (spec && spec.trim().length > 20) {
          return res.json({ success: true, spec, model, provider: 'groq' });
        }
      }

      // Try fallback model on Groq if primary failed
      const fallback = 'llama-3.2-90b-vision-preview';
      const fallbackRes = await fetch('https://api.groq.com/openai/v1/chat/completions', {
        method: 'POST',
        headers: { 'Authorization': `Bearer ${groqApiKey}`, 'Content-Type': 'application/json' },
        body: JSON.stringify({
          model: fallback,
          messages: [{ role: 'user', content: [
            { type: 'text', text: prompt || 'Analyze this image and provide a technical specification.' },
            { type: 'image_url', image_url: { url: imageUrl } }
          ]}],
          max_tokens: 2048
        })
      });
      if (fallbackRes.ok) {
        const fallbackData = await fallbackRes.json();
        const spec = fallbackData.choices?.[0]?.message?.content;
        if (spec && spec.trim().length > 20) {
          return res.json({ success: true, spec, model: fallback, provider: 'groq' });
        }
      }
    } catch (groqErr) {
      console.warn('Groq Vision call error:', groqErr.message);
    }
  }

  // 2. Try Gemini Vision if key is present (or as fallback)
  if (geminiApiKey) {
    try {
      let mimeType = 'image/jpeg';
      let rawData = imageBase64;
      if (imageBase64.startsWith('data:')) {
        const commaIdx = imageBase64.indexOf(',');
        const header = imageBase64.substring(0, commaIdx);
        rawData = imageBase64.substring(commaIdx + 1);
        const match = header.match(/data:([^;]+)/);
        if (match) mimeType = match[1];
      }

      const geminiUrl = `https://generativelanguage.googleapis.com/v1beta/models/gemini-1.5-flash:generateContent?key=${geminiApiKey}`;
      const geminiRes = await fetch(geminiUrl, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          contents: [{
            parts: [
              { text: prompt || 'Analyze this image in detail. Describe UI components, layout, text, colors, code, and errors.' },
              { inline_data: { mime_type: mimeType, data: rawData } }
            ]
          }]
        })
      });

      if (geminiRes.ok) {
        const gData = await geminiRes.json();
        const spec = gData.candidates?.[0]?.content?.parts?.[0]?.text;
        if (spec && spec.trim().length > 20) {
          return res.json({ success: true, spec, model: 'gemini-1.5-flash', provider: 'gemini' });
        }
      }
    } catch (geminiErr) {
      console.warn('Gemini Vision call error:', geminiErr.message);
    }
  }

  return res.status(500).json({ error: 'Vision analysis failed on cloud providers. Falling back to local eyes.' });
});

// ─────────────────────────────────────────────
// API: Training Data Telemetry (Data Flywheel)
// ─────────────────────────────────────────────
app.post('/api/telemetry/submit', async (req, res) => {
  const { token, type, prompt, response: aiResponse, feedback, techStack } = req.body;
  if (!token || !type || !prompt) {
    return res.status(400).json({ error: 'token, type, and prompt are required' });
  }

  const user = await db.getUserByToken(token);
  if (!user) return res.status(403).json({ error: 'Invalid token' });

  if (type === 'accepted_code' || (type === 'feedback' && feedback === 'positive')) {
    await db.addTrainingData({
      id: `td-${Date.now()}`,
      userId: user.id,
      type,
      prompt: prompt.slice(0, 2000),
      response: aiResponse?.slice(0, 5000),
      feedback,
      techStack,
      createdAt: new Date().toISOString()
    });
  }

  res.json({ success: true });
});

// ─────────────────────────────────────────────
// Free Universal Translation Gateway (Zero API Key)
// ─────────────────────────────────────────────

// Helper: Protect code blocks from being translated
function protectCode(text) {
  const codeBlocks = [];
  // Protect triple-backtick blocks
  let protectedText = text.replace(/```[\s\S]*?```/g, (match) => {
    const placeholder = `__CODE_BLOCK_${codeBlocks.length}__`;
    codeBlocks.push(match);
    return placeholder;
  });
  // Protect inline code
  protectedText = protectedText.replace(/`[^`\n]+`/g, (match) => {
    const placeholder = `__CODE_BLOCK_${codeBlocks.length}__`;
    codeBlocks.push(match);
    return placeholder;
  });
  return { protectedText, codeBlocks };
}

function restoreCode(text, codeBlocks) {
  let restored = text;
  codeBlocks.forEach((code, i) => {
    const regex = new RegExp(`__CODE_BLOCK_${i}__`, 'g');
    restored = restored.replace(regex, code);
  });
  return restored;
}

// Fast Unicode language detector (0.001ms)
function detectLanguageScript(text) {
  if (!text || !text.trim()) return 'en';
  // Check Thai Unicode range: \u0E00-\u0E7F
  const thaiMatches = text.match(/[\u0E00-\u0E7F]/g);
  if (thaiMatches && thaiMatches.length >= 2) {
    return 'th';
  }
  // Check CJK (Chinese, Japanese, Korean)
  if (/[\u4E00-\u9FFF\u3040-\u30FF\uAC00-\uD7AF]/.test(text)) {
    return 'cjk';
  }
  // Check Cyrillic (Russian, etc.)
  if (/[\u0400-\u04FF]/.test(text)) {
    return 'ru';
  }
  // Check Arabic
  if (/[\u0600-\u06FF]/.test(text)) {
    return 'ar';
  }
  return 'en';
}

// Free Google Translate GTX RPC call
async function translateWithGoogleGtx(text, from = 'auto', to = 'en') {
  if (!text || !text.trim() || from === to) {
    return { translated: text, detectedLang: from };
  }

  const { protectedText, codeBlocks } = protectCode(text);
  
  if (!protectedText.trim() || (codeBlocks.length > 0 && protectedText.replace(/__CODE_BLOCK_\d+__/g, '').trim().length === 0)) {
    return { translated: text, detectedLang: 'en' };
  }

  const url = `https://translate.googleapis.com/translate_a/single?client=gtx&sl=${from}&tl=${to}&dt=t&q=${encodeURIComponent(protectedText)}`;
  
  const response = await fetch(url, {
    headers: {
      'User-Agent': 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36'
    }
  });

  if (!response.ok) {
    throw new Error(`Translation gateway returned status ${response.status}`);
  }

  const data = await response.json();
  let translatedText = '';
  if (Array.isArray(data[0])) {
    translatedText = data[0].map(item => item[0]).join('');
  } else {
    translatedText = protectedText;
  }

  const detectedLang = data[2] || from;
  const finalText = restoreCode(translatedText, codeBlocks);
  return { translated: finalText, detectedLang };
}

// POST /api/translate  - Translate arbitrary text with code protection
app.post('/api/translate', async (req, res) => {
  const { text, from = 'auto', to = 'en' } = req.body;
  if (!text) {
    return res.status(400).json({ error: 'text is required' });
  }

  try {
    const result = await translateWithGoogleGtx(text, from, to);
    res.json({
      success: true,
      original: text,
      translated: result.translated,
      from: result.detectedLang,
      to
    });
  } catch (err) {
    res.status(500).json({ error: 'Translation failed', details: err.message, fallback: text });
  }
});

// POST /api/translate/two-way  - Convert prompt to English for AI, track if translation back is needed
app.post('/api/translate/two-way', async (req, res) => {
  const { prompt } = req.body;
  if (!prompt) {
    return res.status(400).json({ error: 'prompt is required' });
  }

  const script = detectLanguageScript(prompt);
  if (script === 'en') {
    return res.json({
      promptEn: prompt,
      sourceLang: 'en',
      needsTranslationBack: false
    });
  }

  try {
    const result = await translateWithGoogleGtx(prompt, 'auto', 'en');
    res.json({
      promptEn: result.translated,
      sourceLang: result.detectedLang,
      needsTranslationBack: result.detectedLang !== 'en' && result.detectedLang !== 'th'
    });
  } catch (err) {
    res.json({
      promptEn: prompt,
      sourceLang: script,
      needsTranslationBack: false,
      error: err.message
    });
  }
});

// ─────────────────────────────────────────────
// Serve Windows Desktop Installer / Binary
// ─────────────────────────────────────────────
app.get(['/download/Sent-Code-Setup.exe', '/download/Sent-Code.exe'], (req, res) => {
  const binaryPath = path.resolve(__dirname, '..', '..', 'build', 'bin', 'Sent-Code.exe');
  if (fs.existsSync(binaryPath)) {
    return res.download(binaryPath, 'Sent-Code.exe');
  }
  res.status(404).send('Sent-Code binary not found. Please build Sent-Code first.');
});
// Admin Dashboard & CMS
// ─────────────────────────────────────────────
app.get('/admin', async (req, res) => {
  const users = await db.getAllUsers();
  const totalTrainingEntries = await db.getTrainingDataCount();
  const totalUsers = users.length;
  const defaultTokens = (await db.getSetting('defaultFreeTokens')) || '100';

  const html = `
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>Sent-Code Control Center</title>
  <script src="https://cdn.tailwindcss.com"></script>
</head>
<body class="bg-[#0b0c0e] text-[#e4e6ea] font-sans min-h-screen">
  <div class="max-w-6xl mx-auto px-6 py-8">
    
    <!-- Header -->
    <div class="flex items-center justify-between mb-10 pb-6 border-b border-[#1e2028]">
      <div class="flex items-center gap-3">
        <div class="w-9 h-9 rounded-xl bg-gradient-to-tr from-cyan-500 to-emerald-400 text-gray-950 flex items-center justify-center font-bold text-lg shadow-lg shadow-cyan-500/20">⚡</div>
        <div>
          <h1 class="text-xl font-bold text-white">Sent-Code Control Center</h1>
          <p class="text-xs text-gray-500 mt-0.5">Admin Dashboard & CMS · SQLite Database</p>
        </div>
      </div>
      <div class="flex items-center gap-2">
        <span class="flex items-center gap-1.5 px-3 py-1.5 rounded-full text-xs bg-emerald-950 text-emerald-400 border border-emerald-800/40">
          <span class="w-1.5 h-1.5 rounded-full bg-emerald-400 animate-pulse"></span>Gateway Online
        </span>
        <a href="/api/health" target="_blank" class="px-3 py-1.5 rounded-lg bg-[#17181c] border border-[#26282f] text-xs text-gray-400 hover:text-white transition">API Health</a>
      </div>
    </div>

    <!-- Stats -->
    <div class="grid grid-cols-2 md:grid-cols-4 gap-4 mb-8">
      ${[
        { label: 'Total Users', value: totalUsers, color: 'text-cyan-400' },
        { label: 'Training Samples', value: totalTrainingEntries, color: 'text-emerald-400' },
        { label: 'Default Free Tokens', value: defaultTokens, color: 'text-amber-400' },
        { label: 'Database Engine', value: 'SQLite WAL', color: 'text-purple-400' }
      ].map(s => `
        <div class="bg-[#13141a] p-4 rounded-xl border border-[#1e2028]">
          <div class="text-gray-500 text-[11px] mb-1">${s.label}</div>
          <div class="text-lg font-bold ${s.color}">${s.value}</div>
        </div>
      `).join('')}
    </div>

    <!-- Users Table -->
    <div class="bg-[#13141a] rounded-xl border border-[#1e2028] p-6 mb-6">
      <div class="flex items-center justify-between mb-4">
        <h2 class="text-sm font-semibold text-white">Users & Token Quotas</h2>
        <span class="text-xs text-gray-500">${totalUsers} total</span>
      </div>
      <div class="overflow-x-auto">
        <table class="w-full text-left text-xs">
          <thead>
            <tr class="text-gray-500 border-b border-[#1e2028]">
              <th class="py-2 pr-4 font-medium">Email</th>
              <th class="pr-4 font-medium">Name</th>
              <th class="pr-4 font-medium">Role</th>
              <th class="pr-4 font-medium">Plan</th>
              <th class="pr-4 font-medium">Tokens</th>
              <th class="font-medium">Joined</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-[#1a1c22]">
            ${users.map(u => `
              <tr class="hover:bg-[#1a1c22] transition">
                <td class="py-3 pr-4 font-medium text-white">${u.email}</td>
                <td class="pr-4 text-gray-400">${u.name || '-'}</td>
                <td class="pr-4">
                  <span class="px-2 py-0.5 rounded text-[10px] ${u.role === 'admin' ? 'bg-purple-950 text-purple-300 border border-purple-800/40' : 'bg-[#1e2028] text-gray-400'}">
                    ${u.role}
                  </span>
                </td>
                <td class="pr-4">
                  <span class="px-2 py-0.5 rounded text-[10px] ${u.plan === 'unlimited' ? 'bg-amber-950 text-amber-300 border border-amber-800/40' : 'bg-[#1e2028] text-gray-400'}">
                    ${u.plan || 'free'}
                  </span>
                </td>
                <td class="pr-4 font-mono text-cyan-300 font-semibold">${u.tokens}</td>
                <td class="text-gray-500">${new Date(u.created_at).toLocaleDateString('th-TH')}</td>
              </tr>
            `).join('')}
          </tbody>
        </table>
      </div>
    </div>

    <!-- Training Data Stats -->
    <div class="bg-[#13141a] rounded-xl border border-[#1e2028] p-6">
      <div class="flex items-center justify-between mb-4">
        <h2 class="text-sm font-semibold text-white">🧬 AI Training Data (Data Flywheel)</h2>
        <span class="text-xs text-emerald-400 font-mono">${totalTrainingEntries} gold samples</span>
      </div>
      <div class="text-xs text-gray-500 leading-relaxed">
        ${totalTrainingEntries === 0
          ? 'No training data collected yet. High-quality accepted code snippets from users will appear here for LoRA fine-tuning.'
          : `${totalTrainingEntries} high-quality code pairs collected in SQLite.`}
      </div>
    </div>

  </div>
</body>
</html>`;
  res.send(html);
});

// ─────────────────────────────────────────────
// Serve Astro Static Build
// ─────────────────────────────────────────────
const clientDist = path.join(__dirname, '..', 'client', 'dist');
if (fs.existsSync(clientDist)) {
  app.use(express.static(clientDist));
  app.get('*', (req, res) => {
    res.sendFile(path.join(clientDist, 'index.html'));
  });
}

// ─────────────────────────────────────────────
// Start Server
// ─────────────────────────────────────────────
await db.init();

app.listen(PORT, () => {
  console.log(`\n⚡ Sent-Code Web Server running at http://localhost:${PORT}`);
  console.log(`⚡ Database     → ${db.mode === 'mysql' ? 'MySQL / MariaDB (Docker)' : 'SQLite WAL Mode'}`);
  console.log(`⚡ phpMyAdmin   → http://localhost:8080`);
  console.log(`⚡ Admin CMS    → http://localhost:${PORT}/admin`);
  console.log(`⚡ API Health   → http://localhost:${PORT}/api/health`);
  console.log(`⚡ Vision API   → POST http://localhost:${PORT}/api/vision/analyze`);
  console.log(`⚡ Translate    → POST http://localhost:${PORT}/api/translate`);
  console.log(`⚡ Auth API     → POST http://localhost:${PORT}/api/auth/login`);
  console.log(`⚡ Download     → GET  http://localhost:${PORT}/download/Sent-Code.exe\n`);
});
