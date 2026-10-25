import React, { useState, useEffect, useRef, useMemo, useCallback } from 'react';
import { createPortal } from 'react-dom';
import {
  FolderOpen,
  Plus,
  History,
  Clock,
  Settings,
  Image as ImageIcon,
  Send,
  Terminal,
  ChevronRight,
  ChevronDown,
  X,
  Play,
  RotateCcw,
  ExternalLink,
  ShieldCheck,
  CheckCircle2,
  AlertTriangle,
  Copy,
  ThumbsUp,
  ThumbsDown,
  Sparkles,
  Cpu,
  Globe,
  Radio,
  FileCode,
  FileText,
  Folder,
  Zap,
  Brain,
  Flame,
  Check,
  Sliders,
  Palette,
  Layers,
  Shield,
  HelpCircle,
  MessageSquare,
  Search,
  User,
  Compass,
  Trash2,
  Square,
  Download,
  HardDrive,
  Eye,
  GitBranch,
  ArrowUp,
  Cloud,
  PanelLeft,
  ChevronLeft,
  Minus
} from 'lucide-react';

import {
  SelectProjectFolder,
  SetCurrentFolder,
  GetProjectBranch,
  GetRecentWorkspaces,
  ListProjectFiles,
  RunBuildCommand,
  DetectBuildCommand,
  SendChatMessage,
  StopActiveChat,
  CheckQuotaStatus,
  RefillTokens,
  SetOnlineStatus,
  GetGrokAccountInfo,
  GetGrokQuotaUsage,
  GetGrokSessions,
  LoadGrokSessionMessages,
  DeleteGrokSession,
  GetCacheStats,
  ClearOldSessions,
  StartGrokDeviceLogin,
  CancelGrokLogin,
  LogoutGrok,
  ListModels,
  AddModel,
  RemoveModel,
  InstallModel,
  CancelModelInstall,
  UninstallModel,
  GetEngineStatus,
  GetClaudeAccountInfo,
  GetGatewayStatus,
  GetGatewayProfile,
  LoginGateway,
  InstallEngine,
  CancelEngineInstall,
  UninstallEngine,
  CheckModelCompatibility,
  CheckSystemSpecs,
  GetVisionStatus,
  SaveVisionConfig,
} from '../wailsjs/go/main/App';
import { EventsOn, BrowserOpenURL, WindowMinimise, WindowToggleMaximise, Quit } from '../wailsjs/runtime/runtime';
import { main } from '../wailsjs/go/models';

export interface AgentAction {
  type: string;
  label: string;
  detail?: string;
  lineRange?: string;
  icon?: string;
}

export interface LiveAgentStep {
  id: string;
  icon: string;
  title: string;
  detail?: string;
  status: 'pending' | 'running' | 'done';
  timestamp: number;
}

export interface DiffStats {
  additions: number;
  deletions: number;
  files?: string[];
  diffText?: string;
}

interface Message {
  id: string;
  sender: 'user' | 'assistant';
  text: string;
  thought?: string;
  actions?: AgentAction[];
  images?: string[];
  commandRan?: main.CommandResult;
  status?: string;
  timestamp: string;
  // Which engine produced an assistant message (e.g. "grok-auto", "qwen-coder-7b"). Lets the
  // backend hand context over correctly when the user switches models mid-conversation.
  model?: string;
  diffStats?: DiffStats;
}

export interface ChatSession {
  id: string;
  title: string;
  projectFolder: string;
  updatedAt: number;
  messages: Message[];
}

const SESSIONS_STORAGE_KEY = 'sent_code_sessions_v1';
const ACTIVE_SESSION_STORAGE_KEY = 'sent_code_active_session_id';

const getInitialSessions = (): { sessions: ChatSession[]; activeId: string } => {
  let existingSessions: ChatSession[] = [];
  try {
    const raw = localStorage.getItem(SESSIONS_STORAGE_KEY);
    if (raw) {
      const parsed: ChatSession[] = JSON.parse(raw);
      if (Array.isArray(parsed) && parsed.length > 0) {
        // Keep previous sessions that actually have messages so we don't accumulate blank ones
        existingSessions = parsed.filter(s => s.messages && s.messages.length > 0);
      }
    }
  } catch (e) {
    console.error('Failed to load sessions from storage', e);
  }

  // Always start with a clean, fresh conversation when opening the program
  const newId = `session_${Date.now()}`;
  const freshSession: ChatSession = {
    id: newId,
    title: 'New Conversation',
    projectFolder: 'C:\\Drive-D\\My-Program\\Sent-Code',
    updatedAt: Date.now(),
    messages: []
  };

  try {
    localStorage.setItem(ACTIVE_SESSION_STORAGE_KEY, newId);
  } catch (e) {}

  return {
    sessions: [freshSession, ...existingSessions],
    activeId: newId,
  };
};

// Renders `children` through a portal straight into <body>, fixed-positioned to the right of
// triggerRef's element and bottom-aligned to it — a side flyout, like the old
// `absolute left-full bottom-0`, but outside any scrollable ancestor so that ancestor's
// overflow-y-auto can never clip it.
function TriggerFlyout({ open, triggerRef, className, children }: {
  open: boolean;
  triggerRef: React.RefObject<HTMLElement | null>;
  className?: string;
  children: React.ReactNode;
}) {
  const [pos, setPos] = useState<{ left: number; bottom: number } | null>(null);

  useEffect(() => {
    if (!open || !triggerRef.current) {
      setPos(null);
      return;
    }
    const update = () => {
      const r = triggerRef.current?.getBoundingClientRect();
      if (r) setPos({ left: r.right + 6, bottom: window.innerHeight - r.bottom });
    };
    update();
    window.addEventListener('resize', update);
    return () => window.removeEventListener('resize', update);
  }, [open, triggerRef]);

  if (!open || !pos) return null;
  return createPortal(
    <div className={className} style={{ position: 'fixed', left: pos.left, bottom: pos.bottom }}>
      {children}
    </div>,
    document.body
  );
}

export function App() {
  // Session Persistence State
  const initialData = useMemo(() => getInitialSessions(), []);
  const [sessions, setSessions] = useState<ChatSession[]>(initialData.sessions);
  const [activeSessionId, setActiveSessionId] = useState<string>(initialData.activeId);
  const [isSidebarOpen, setIsSidebarOpen] = useState<boolean>(true);

  // State
  const [projectFolder, setProjectFolder] = useState<string>(() => {
    return localStorage.getItem('ah_code_current_project') || 'C:\\Drive-D\\My-Program\\Sent-Code';
  });
  const [projectBranch, setProjectBranch] = useState<string>('master');
  const [recentWorkspaces, setRecentWorkspaces] = useState<main.WorkspaceItem[]>([]);
  const [isWorkspaceDropdownOpen, setIsWorkspaceDropdownOpen] = useState<boolean>(false);
  const [workspaceSearch, setWorkspaceSearch] = useState<string>('');
  const [agentApprovalMode, setAgentApprovalMode] = useState<string>('Ask before changes');
  const [isApprovalDropdownOpen, setIsApprovalDropdownOpen] = useState<boolean>(false);
  const [projectFiles, setProjectFiles] = useState<main.ProjectFileItem[]>([]);

  const filteredWorkspaces = useMemo(() => {
    if (!workspaceSearch.trim()) return recentWorkspaces;
    const q = workspaceSearch.toLowerCase();
    return recentWorkspaces.filter(ws =>
      (ws.name || '').toLowerCase().includes(q) || (ws.path || '').toLowerCase().includes(q)
    );
  }, [recentWorkspaces, workspaceSearch]);
  // 'grok-fast' | 'grok-auto' | 'grok-expert' | 'grok-heavy' or the id of any model in the registry ('qwen-coder', 'custom-...')
  type ModelMode = string;
  type GrokTier = 'NONE' | 'FREE' | 'SUPERGROK LITE' | 'SUPERGROK' | 'SUPERGROK PLUS' | 'SUPERGROK HEAVY' | string;
  const [selectedModel, setSelectedModelRaw] = useState<ModelMode>(() => {
    const saved = localStorage.getItem('sent_code_selected_model');
    if (!saved || saved === 'sent-code-3b' || saved === 'qwen-coder-3b') {
      return 'qwen-coder-7b';
    }
    return saved;
  });
  // Wrapper that persists model selection to localStorage so it survives page reloads
  // and so loadGrokAccount can check whether the user already has a preference.
  const setSelectedModel = (m: ModelMode) => {
    localStorage.setItem('sent_code_selected_model', m);
    setSelectedModelRaw(m);
  };
  const [grokTier, setGrokTier] = useState<GrokTier>('NONE');
  const [userEmail, setUserEmail] = useState<string>('');
  const [isSyncingTier, setIsSyncingTier] = useState<boolean>(false);
  const [inputText, setInputText] = useState<string>('');
  const [attachedImages, setAttachedImages] = useState<string[]>([]);
  const [previewImage, setPreviewImage] = useState<string | null>(null);
  const [copiedMsgId, setCopiedMsgId] = useState<string | null>(null);

  const copyToClipboard = (text: string, id?: string) => {
    if (!text) return;
    const markCopied = () => {
      if (id) {
        setCopiedMsgId(id);
        setTimeout(() => setCopiedMsgId(null), 2000);
      }
    };
    if (navigator.clipboard && window.isSecureContext) {
      navigator.clipboard.writeText(text).then(markCopied).catch(() => {
        fallbackCopy(text, markCopied);
      });
    } else {
      fallbackCopy(text, markCopied);
    }
  };

  const fallbackCopy = (text: string, onSuccess?: () => void) => {
    try {
      const textArea = document.createElement("textarea");
      textArea.value = text;
      textArea.style.position = "fixed";
      textArea.style.left = "-999999px";
      textArea.style.top = "-999999px";
      document.body.appendChild(textArea);
      textArea.focus();
      textArea.select();
      const successful = document.execCommand('copy');
      document.body.removeChild(textArea);
      if (successful && onSuccess) onSuccess();
    } catch (e) {
      console.error("Clipboard copy fallback error:", e);
    }
  };

  const [messages, setMessages] = useState<Message[]>(() => {
    const act = initialData.sessions.find(s => s.id === initialData.activeId);
    return act ? act.messages : [];
  });
  const [loading, setLoading] = useState<boolean>(false);
  const [tokensRemaining, setTokensRemaining] = useState<number>(100);
  const [isOnline, setIsOnline] = useState<boolean>(true);
  const [isSettingsOpen, setIsSettingsOpen] = useState<boolean>(false);
  type SettingsTab = 'general' | 'application' | 'appearance' | 'models' | 'customizations' | 'browser';
  const [settingsTab, setSettingsTab] = useState<SettingsTab>('general');
  const [queuedMessages, setQueuedMessages] = useState<'queue' | 'send_immediately'>('queue');
  const [securityPreset, setSecurityPreset] = useState<string>('Default');
  const [planReviewPolicy, setPlanReviewPolicy] = useState<string>('Always Ask');
  const [activeThoughtElapsed, setActiveThoughtElapsed] = useState<number>(1);
  const [activeLoadingContext, setActiveLoadingContext] = useState<{
    type: 'file' | 'image' | 'reasoning';
    text: string;
    file?: string;
    icon?: string;
  }>({ type: 'reasoning', text: 'Thinking...' });
  const [liveSteps, setLiveSteps] = useState<LiveAgentStep[]>([]);
  const liveStepsRef = useRef<LiveAgentStep[]>([]);
  const [liveThoughtText, setLiveThoughtText] = useState<string>('');
  const [isLiveThoughtOpen, setIsLiveThoughtOpen] = useState<boolean>(false);
  const [openThoughts, setOpenThoughts] = useState<Record<string, boolean>>({ '2': true });
  const [isModelDropdownOpen, setIsModelDropdownOpen] = useState<boolean>(false);
  const [loginModalOpen, setLoginModalOpen] = useState<boolean>(false);
  const [loginSession, setLoginSession] = useState<main.LoginSessionInfo | null>(null);
  const [loginPending, setLoginPending] = useState<boolean>(false);
  const [loginSuccessMessage, setLoginSuccessMessage] = useState<string>('');
  const [cacheWarning, setCacheWarning] = useState<main.CacheStats | null>(null);
  const [grokQuota, setGrokQuota] = useState<main.GrokQuotaUsage | null>(null);

  // Web Gateway User Authentication
  const [gatewayUser, setGatewayUser] = useState<main.GatewayUser | null>(null);
  const [isAuthModalOpen, setIsAuthModalOpen] = useState<boolean>(false);
  const [loginEmail, setLoginEmail] = useState<string>('');
  const [loginLoading, setLoginLoading] = useState<boolean>(false);
  const [loginError, setLoginError] = useState<string>('');

  // Local engine + model registry
  type Progress = { stage: string; percent: number; done: number; total: number; message: string };
  const [models, setModels] = useState<main.ModelStatus[]>([]);
  const [engine, setEngine] = useState<main.EngineStatus | null>(null);
  const [gateway, setGateway] = useState<main.GatewayStatus | null>(null);
  const [claudeAccount, setClaudeAccount] = useState<main.ClaudeAccountInfo | null>(null);
  const [engineProgress, setEngineProgress] = useState<Progress | null>(null);
  const [modelProgress, setModelProgress] = useState<Record<string, Progress>>({});
  const [modelError, setModelError] = useState<string>('');
  const [addModelOpen, setAddModelOpen] = useState<boolean>(false);
  const [newModel, setNewModel] = useState({ kind: 'ollama', name: '', tag: '', command: '', args: '', vision: false });
  // Side flyout submenus. Rendered through a portal (see FlyoutPortal below) straight into
  // <body>, positioned by measuring the trigger row — not nested inside the dropdown's own
  // scrollable box, so the dropdown's overflow-y-auto can never clip them.
  const [isGrokSubmenuOpen, setIsGrokSubmenuOpen] = useState<boolean>(false);
  const grokTriggerRef = useRef<HTMLDivElement>(null);
  const [isQwenSubmenuOpen, setIsQwenSubmenuOpen] = useState<boolean>(false);
  const qwenTriggerRef = useRef<HTMLDivElement>(null);
  const [compatibilityModal, setCompatibilityModal] = useState<main.ModelCompatibility | null>(null);
  const [isCheckingCompat, setIsCheckingCompat] = useState<boolean>(false);

  // Grok reasoning tiers for the side flyout submenu
  const grokTiers = useMemo(() => [
    { id: 'grok-fast', label: 'Fast', speedTag: 'Fastest', desc: 'Quick edits & simple questions', icon: Zap, color: 'text-amber-400', badgeColor: 'bg-amber-950 text-amber-400 border border-amber-800/40' },
    { id: 'grok-auto', label: 'Auto', speedTag: 'Balanced', desc: 'Balanced intelligent routing for everyday coding', icon: Sparkles, color: 'text-cyan-400', badgeColor: 'bg-cyan-950 text-cyan-400 border border-cyan-800/40' },
    { id: 'grok-expert', label: 'Expert', speedTag: 'Deeper', desc: 'Deep reasoning & complex architecture', icon: Brain, color: 'text-purple-400', badgeColor: 'bg-purple-950 text-purple-400 border border-purple-800/40' },
    { id: 'grok-heavy', label: 'Heavy', speedTag: 'Max effort', desc: 'Maximum compute for hardest coding tasks', icon: Flame, color: 'text-rose-400', badgeColor: 'bg-rose-950 text-rose-400 border border-rose-800/40' },
  ], []);

  // Vision Eyes State (ดวงตาเอไอ)
  const [visionStatus, setVisionStatus] = useState<main.VisionStatus | null>(null);
  const [groqKeyInput, setGroqKeyInput] = useState<string>('');
  const [visionSaveMsg, setVisionSaveMsg] = useState<string>('');
  const [isSavingVision, setIsSavingVision] = useState<boolean>(false);

  // Remote Ollama / Cloud GPU Server State (Google Colab / A100)
  const [remoteOllamaStatus, setRemoteOllamaStatus] = useState<{
    configured: boolean;
    connected: boolean;
    url: string;
    models?: string[];
    error?: string;
  }>({ configured: false, connected: false, url: '' });
  const [remoteOllamaInput, setRemoteOllamaInput] = useState<string>('');
  const [isTestingRemote, setIsTestingRemote] = useState<boolean>(false);
  const [remoteSaveMsg, setRemoteSaveMsg] = useState<string>('');
  const [showColabGuide, setShowColabGuide] = useState<boolean>(false);
  const [copiedColabCode, setCopiedColabCode] = useState<boolean>(false);

  const colabPythonScript = `# ==============================================================================
# One-Click Setup: Ollama + Qwen 2.5 Coder 32B + Cloudflare Tunnel
!apt-get update -qq && apt-get install -y -qq zstd
!curl -fsSL https://ollama.com/install.sh | sh
!curl -s -Lo /usr/local/bin/cloudflared https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64
!chmod +x /usr/local/bin/cloudflared

import os, subprocess, time, threading, urllib.request, re

env = os.environ.copy()
env["OLLAMA_HOST"] = "0.0.0.0:11434"
env["OLLAMA_ORIGINS"] = "*"
subprocess.Popen(["ollama", "serve"], env=env)

# รอให้ Ollama เริ่มทำงาน
for _ in range(15):
    try:
        with urllib.request.urlopen("http://localhost:11434/") as resp:
            if resp.status == 200: break
    except: time.sleep(1)

# ดึงโมเดล Qwen 2.5 Coder 32B (A100 โหลดเร็วมาก)
print("⬇️ กำลังดาวน์โหลด Qwen 2.5 Coder 32B เข้าการ์ดจอ...")
subprocess.run(["ollama", "pull", "qwen2.5-coder:32b"], check=True)
print("✅ โหลดโมเดล 32B สำเร็จแล้ว!")

# เปิด Cloudflare Tunnel ให้โปรแกรม AH-Code เชื่อมต่อ
print("\\n🌐 กำลังสร้าง URL สำหรับเชื่อมต่อ...")
proc = subprocess.Popen(["cloudflared", "tunnel", "--url", "http://localhost:11434"],
                        stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)

for line in iter(proc.stdout.readline, ''):
    m = re.search(r'https://[a-zA-Z0-9-]+\\.trycloudflare\\.com', line)
    if m:
        url = m.group(0)
        print("\\n" + "="*60)
        print("🎉 สำเร็จ! คัดลอกลิงก์นี้ไปวางในโปรแกรม AH-Code:")
        print(f"👉 {url}")
        print("="*60 + "\\n")
        break
`;

  const refreshRemoteOllamaStatus = useCallback(async () => {
    try {
      const app = (window as any).go?.main?.App;
      if (app?.GetRemoteOllamaStatus) {
        const st = await app.GetRemoteOllamaStatus();
        if (st) {
          setRemoteOllamaStatus(st);
          if (st.url) {
            setRemoteOllamaInput(st.url);
          }
        }
      }
    } catch (e) {
      console.error('Failed to get remote ollama status', e);
    }
  }, []);

  const handleSaveRemoteOllama = async () => {
    setIsTestingRemote(true);
    setRemoteSaveMsg('');
    try {
      const app = (window as any).go?.main?.App;
      if (app?.SetRemoteOllamaURL) {
        const st = await app.SetRemoteOllamaURL(remoteOllamaInput.trim());
        setRemoteOllamaStatus(st);
        if (st.connected) {
          setRemoteSaveMsg(`✓ เชื่อมต่อสำเร็จ! พบโมเดล ${st.models?.length || 0} ตัวบน Cloud GPU`);
        } else if (st.configured) {
          setRemoteSaveMsg(`⚠️ บันทึกแล้ว แต่ยังเชื่อมต่อไม่ได้: ${st.error || 'โปรดตรวจสอบลิงก์'}`);
        } else {
          setRemoteSaveMsg('สลับกลับมาใช้ Local Ollama ในเครื่องแล้ว');
        }
        refreshModels();
      }
    } catch (e: any) {
      setRemoteSaveMsg(`เกิดข้อผิดพลาด: ${e?.message || e}`);
    } finally {
      setIsTestingRemote(false);
    }
  };

  const handleDisconnectRemoteOllama = async () => {
    setIsTestingRemote(true);
    try {
      setRemoteOllamaInput('');
      const app = (window as any).go?.main?.App;
      if (app?.SetRemoteOllamaURL) {
        const st = await app.SetRemoteOllamaURL('');
        setRemoteOllamaStatus(st);
        setRemoteSaveMsg('ยกเลิกการเชื่อมต่อแล้ว กลับมาใช้ Local Ollama');
        refreshModels();
      }
    } catch (e: any) {
      setRemoteSaveMsg(`เกิดข้อผิดพลาด: ${e?.message || e}`);
    } finally {
      setIsTestingRemote(false);
    }
  };

  const qwenTiers = useMemo(() => [
    {
      id: 'qwen-coder-7b',
      label: '7B',
      title: 'Qwen-Coder 7B',
      speedTag: 'Balanced',
      size: '~4.4 GB',
      desc: 'High accuracy for complex code',
    },
    {
      id: 'qwen-coder-14b',
      label: '14B',
      title: 'Qwen-Coder 14B',
      speedTag: 'Deep Logic',
      size: '~9.0 GB',
      desc: 'Architectural reasoning & logic',
    },
    {
      id: 'qwen-coder-32b',
      label: '32B',
      title: 'Qwen-Coder 32B',
      speedTag: 'Expert Heavy',
      size: '~20 GB',
      desc: 'Heavyweight server-grade AI',
    },
  ], []);

  const isTierInstalled = (id: string) => {
    const found = models.find(m => m.id === id);
    return !!found?.installed;
  };

  const isTierActive = (id: string) => {
    if (selectedModel === id) return true;
    if ((selectedModel === 'qwen-coder' || selectedModel === 'qwen-coder-7b' || selectedModel === 'sent-code-3b' || selectedModel === 'qwen-coder-3b') && id === 'qwen-coder-7b') return true;
    return false;
  };

  // Claude Code: same hierarchical parent-with-tiers pattern as Qwen-Coder above, but the
  // "tiers" pick which underlying Claude model answers rather than a weight size to download —
  // there's nothing to install here beyond the `claude` CLI itself being on PATH.
  const [isClaudeSubmenuOpen, setIsClaudeSubmenuOpen] = useState<boolean>(false);
  const claudeTriggerRef = useRef<HTMLDivElement>(null);

  // Same model throughout; each tier is a --effort level (verified against a real `claude`
  // install — see models.go). Low/Medium/High/Ultra map to --effort low/medium/high/max.
  const claudeTiers = useMemo(() => [
    { id: 'claude-code-low', label: 'Low', title: 'Claude Code — Low', speedTag: 'Fastest', desc: 'Quick edits & simple questions' },
    { id: 'claude-code-medium', label: 'Medium', title: 'Claude Code — Medium', speedTag: 'Balanced', desc: 'Everyday coding' },
    { id: 'claude-code-high', label: 'High', title: 'Claude Code — High', speedTag: 'Deeper', desc: 'Harder problems' },
    { id: 'claude-code-ultra', label: 'Ultra', title: 'Claude Code — Ultra', speedTag: 'Max effort', desc: 'Hardest, architectural problems' },
  ], []);

  const [viewingDiff, setViewingDiff] = useState<DiffStats | null>(null);

  const effortLabels = ['Low', 'Medium', 'High', 'Ultra'];
  const getEffortIndex = (model: string): number => {
    if (model.includes('fast') || model.includes('low') || model.includes('3b')) return 0;
    if (model.includes('auto') || model.includes('medium') || model.includes('7b')) return 1;
    if (model.includes('expert') || model.includes('high') || model.includes('14b')) return 2;
    if (model.includes('heavy') || model.includes('ultra') || model.includes('32b')) return 3;
    return 1;
  };
  const effortIndex = getEffortIndex(selectedModel);

  const handleSetEffort = (idx: number) => {
    if (selectedModel.startsWith('grok-') || grokTiers.some(t => t.id === selectedModel)) {
      const grokIds = ['grok-fast', 'grok-auto', 'grok-expert', 'grok-heavy'];
      setSelectedModel(grokIds[idx]);
    } else if (selectedModel.startsWith('claude-code')) {
      const claudeIds = ['claude-code-low', 'claude-code-medium', 'claude-code-high', 'claude-code-ultra'];
      setSelectedModel(claudeIds[idx]);
    } else if (selectedModel.includes('qwen') || selectedModel.includes('sent-code')) {
      const qwenIds = ['qwen-coder-7b', 'qwen-coder-7b', 'qwen-coder-14b', 'qwen-coder-32b'];
      const target = qwenIds[idx];
      if (isTierInstalled(target)) {
        setSelectedModel(target);
      } else {
        handleCheckAndInstall(target);
      }
    }
  };

  const chatEndRef = useRef<HTMLDivElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const userCancelledRef = useRef<boolean>(false);

  // Timer for live agent thinking counter
  useEffect(() => {
    let timer: any;
    if (loading) {
      setActiveThoughtElapsed(1);
      timer = setInterval(() => {
        setActiveThoughtElapsed(prev => prev + 1);
      }, 1000);
    }
    return () => clearInterval(timer);
  }, [loading]);

  // Initial load
  useEffect(() => {
    loadFiles(projectFolder);
    updateQuota();
    loadGrokAccount();
    loadGrokSessionsHistory(projectFolder);

    GetRecentWorkspaces().then(list => {
      if (list && list.length > 0) {
        setRecentWorkspaces(list);
      }
    }).catch(() => {});

    if (projectFolder) {
      GetProjectBranch(projectFolder).then(b => {
        if (b) setProjectBranch(b);
      }).catch(() => {});
    }

    // Check saved gateway session token
    const savedToken = localStorage.getItem('sent_code_session_token');
    if (savedToken) {
      GetGatewayProfile(savedToken).then(u => {
        if (u) {
          setGatewayUser(u);
          setTokensRemaining(u.tokens);
        }
      }).catch(() => {});
    }

    // Listen for code event as soon as Go backend extracts it
    EventsOn('grok-login-code', (session: any) => {
      setLoginSession(session);
      setLoginPending(false);
    });

    // Listen for OAuth login completion from Go backend
    EventsOn('grok-login-complete', (info: any) => {
      setLoginPending(false);
      const tierName = info.tier && info.tier !== 'CLOUD' ? info.tier : 'FREE';
      setUserEmail(info.email || '');
      setLoginSuccessMessage(`Connected as ${info.email || 'SuperGrok User'} (${tierName})`);
      setGrokTier(tierName as any);
      setSelectedModel('grok-auto');
      setTimeout(() => {
        setLoginModalOpen(false);
        setLoginSuccessMessage('');
      }, 2000);
    });

    refreshModels();

    // Local engine download progress
    const offEngine = EventsOn('engine-install-progress', (p: any) => {
      setEngineProgress({ stage: p.stage, percent: p.percent, done: p.downloaded, total: p.total, message: p.message });
      if (p.stage === 'done' || p.stage === 'error' || p.stage === 'cancelled') {
        refreshModels();
        if (p.stage === 'done') setTimeout(() => setEngineProgress(null), 1500);
      }
    });

    // Model download progress
    const offModel = EventsOn('model-install-progress', (p: any) => {
      setModelProgress(prev => ({
        ...prev,
        [p.id]: { stage: p.stage, percent: p.percent, done: p.completed, total: p.total, message: p.message },
      }));
      if (p.stage === 'done' || p.stage === 'error' || p.stage === 'cancelled') {
        refreshModels();
        if (p.stage === 'done') {
          setTimeout(() => setModelProgress(prev => { const n = { ...prev }; delete n[p.id]; return n; }), 1500);
        }
      }
    });

    // Clipboard paste listener for screenshots
    const handlePaste = (e: ClipboardEvent) => {
      const items = e.clipboardData?.items;
      if (!items) return;

      for (let i = 0; i < items.length; i++) {
        if (items[i].type.indexOf('image') !== -1) {
          const file = items[i].getAsFile();
          if (file) {
            const reader = new FileReader();
            reader.onload = (event) => {
              if (event.target?.result) {
                setAttachedImages(prev => [...prev, event.target!.result as string]);
              }
            };
            reader.readAsDataURL(file);
          }
        }
      }
    };

    // Real-time live agent steps (Antigravity-style trajectory)
    const offLiveStep = EventsOn('chat-live-step', (step: LiveAgentStep) => {
      const current = liveStepsRef.current;
      const idx = current.findIndex(s => s.id === step.id);
      if (idx !== -1) {
        current[idx] = step;
      } else {
        current.push(step);
      }
      setLiveSteps([...current]);
    });

    // Real-time live thinking stream
    const offThoughtChunk = EventsOn('chat-thought-chunk', (chunk: string) => {
      setLiveThoughtText(prev => prev + chunk);
    });

    window.addEventListener('paste', handlePaste);
    return () => {
      window.removeEventListener('paste', handlePaste);
      offEngine();
      offModel();
      offLiveStep();
      offThoughtChunk();
    };
  }, []);

  useEffect(() => {
    chatEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [messages, loading]);

  const refreshModels = async () => {
    try {
      const [list, eng] = await Promise.all([ListModels(), GetEngineStatus()]);
      setModels(list || []);
      setEngine(eng);
    } catch (e) {
      console.error('Failed to load models:', e);
    }
    // Separate from the list above: an unreachable gateway takes a few seconds to time out
    GetGatewayStatus().then(setGateway).catch(() => setGateway(null));
    // Also separate: shells out to `claude auth status`, so it's slower than reading local state
    GetClaudeAccountInfo().then(setClaudeAccount).catch(() => setClaudeAccount(null));
    // Query Vision Eyes status (Groq cloud key + local vision models)
    GetVisionStatus().then(setVisionStatus).catch(() => setVisionStatus(null));
    refreshRemoteOllamaStatus();
  };

  const handleSaveVisionConfig = async () => {
    if (!groqKeyInput.trim()) return;
    setIsSavingVision(true);
    setVisionSaveMsg('');
    try {
      await SaveVisionConfig(groqKeyInput.trim(), '', 'llama-3.2-11b-vision-preview');
      setVisionSaveMsg('บันทึกคีย์สำเร็จแล้ว! ดวงตา AI พร้อมทำงาน');
      setGroqKeyInput('');
      const st = await GetVisionStatus();
      setVisionStatus(st);
      setTimeout(() => setVisionSaveMsg(''), 4000);
    } catch (e: any) {
      setVisionSaveMsg(`บันทึกไม่สำเร็จ: ${e?.message || e}`);
    } finally {
      setIsSavingVision(false);
    }
  };

  const formatBytes = (n: number) => {
    if (!n) return '0 MB';
    if (n >= 1024 ** 3) return `${(n / 1024 ** 3).toFixed(2)} GB`;
    return `${Math.round(n / 1024 ** 2)} MB`;
  };

  const handleInstallEngine = async () => {
    setModelError('');
    setEngineProgress({ stage: 'resolving', percent: 0, done: 0, total: 0, message: 'Contacting update service...' });
    try {
      await InstallEngine();
    } catch (e: any) {
      setEngineProgress(null);
      setModelError(String(e?.message || e));
    }
  };

  const handleCheckAndInstall = async (modelId: string) => {
    setIsCheckingCompat(true);
    try {
      const compat = await CheckModelCompatibility(modelId);
      setCompatibilityModal(compat);
    } catch (e: any) {
      console.error('Failed to check compatibility:', e);
      handleInstallModel(modelId);
    } finally {
      setIsCheckingCompat(false);
    }
  };

  const handleInstallModel = async (id: string) => {
    setModelError('');
    setModelProgress(prev => ({ ...prev, [id]: { stage: 'pulling', percent: 0, done: 0, total: 0, message: 'Starting engine...' } }));
    try {
      await InstallModel(id);
    } catch (e: any) {
      setModelProgress(prev => { const n = { ...prev }; delete n[id]; return n; });
      setModelError(String(e?.message || e));
    }
  };

  const handleUninstallModel = async (m: main.ModelStatus) => {
    if (!window.confirm(`Delete downloaded files for "${m.name}"? You can download it again later.`)) return;
    setModelError('');
    try {
      await UninstallModel(m.id);
    } catch (e: any) {
      setModelError(String(e?.message || e));
    }
    refreshModels();
  };

  const handleRemoveModel = async (m: main.ModelStatus) => {
    setModelError('');
    try {
      await RemoveModel(m.id);
      if (selectedModel === m.id) setSelectedModel('qwen-coder-7b');
    } catch (e: any) {
      setModelError(String(e?.message || e));
    }
    refreshModels();
  };

  const handleAddModel = async () => {
    setModelError('');
    try {
      const added = await AddModel(main.ModelDef.createFrom({
        kind: newModel.kind,
        name: newModel.name,
        ollamaTag: newModel.kind === 'ollama' ? newModel.tag : '',
        command: newModel.kind === 'cli' ? newModel.command : '',
        // one argument per line; {prompt} is replaced by the user's message
        args: newModel.kind === 'cli' ? newModel.args.split('\n') : [],
        supportsVision: newModel.kind === 'ollama' && newModel.vision,
        builtin: false,
      }));
      setNewModel({ kind: newModel.kind, name: '', tag: '', command: '', args: '', vision: false });
      setAddModelOpen(false);
      await refreshModels();
      if (added.installed) setSelectedModel(added.id);
    } catch (e: any) {
      setModelError(String(e?.message || e));
    }
  };

  const loadGrokQuota = async (folder: string) => {
    try {
      const q = await GetGrokQuotaUsage(folder);
      setGrokQuota(q);
    } catch (e) {
      console.error('Failed to load Grok quota usage:', e);
    }
  };

  const loadGrokAccount = async () => {
    setIsSyncingTier(true);
    try {
      const info = await GetGrokAccountInfo();
      if (info.isLoggedIn) {
        setUserEmail(info.email || '');
        setGrokTier((info.tier && info.tier !== 'CLOUD' ? info.tier : 'FREE') as any);
        // Only default to grok-auto on first login (no saved preference).
        // If the user had already chosen another model and reloads, respect that choice.
        const savedModel = localStorage.getItem('sent_code_selected_model');
        if (!savedModel) {
          setSelectedModel('grok-auto');
        }
        await loadGrokQuota(projectFolder);
      } else {
        setUserEmail('');
        setGrokTier('NONE');
        setGrokQuota(null);
      }
    } catch (e) {
      console.error(e);
    } finally {
      setIsSyncingTier(false);
    }
  };

  const handleTriggerLogin = async () => {
    setLoginModalOpen(true);
    setLoginPending(true);
    setLoginSuccessMessage('');
    try {
      const session = await StartGrokDeviceLogin();
      setLoginSession(session);
    } catch (err: any) {
      console.error(err);
      setLoginPending(false);
    }
  };

  const handleLogout = async () => {
    try {
      const info = await LogoutGrok();
      if (!info.isLoggedIn) {
        setUserEmail('');
        setGrokTier('NONE');
        setSelectedModel('qwen-coder-7b');
        setLoginSession(null);
      }
    } catch (e) {
      console.error(e);
    }
  };

  const renderStepIcon = (icon?: string) => {
    switch (icon) {
      case 'file':
      case 'read':
        return <FileText size={13} className="text-cyan-400 shrink-0" />;
      case 'edit':
      case 'write':
        return <FileCode size={13} className="text-amber-400 shrink-0" />;
      case 'code':
        return <FileCode size={13} className="text-cyan-400 shrink-0" />;
      case 'terminal':
      case 'cmd':
        return <Terminal size={13} className="text-emerald-400 shrink-0" />;
      case 'search':
        return <Search size={13} className="text-sky-400 shrink-0" />;
      case 'brain':
        return <Brain size={13} className="text-purple-400 shrink-0" />;
      case 'image':
        return <ImageIcon size={13} className="text-rose-400 shrink-0" />;
      case 'check':
        return <Check size={13} className="text-emerald-400 shrink-0" />;
      default:
        return <FileText size={13} className="text-cyan-400 shrink-0" />;
    }
  };

  const getActionHeader = (actions?: AgentAction[], thought?: string) => {
    const thoughtSummary = thought ? thought.split('\n')[0].trim() : 'Thought for a few seconds';
    if (!actions || actions.length === 0) {
      return thoughtSummary;
    }
    const fileActions = actions.filter(a => a.type === 'read_file');
    if (fileActions.length > 0) {
      return `Exploring ${fileActions.length} file${fileActions.length > 1 ? 's' : ''}`;
    }
    const imgAction = actions.find(a => a.type === 'view_image');
    if (imgAction) {
      return `Analyzed image context ${imgAction.detail || ''}`;
    }
    const cmdAction = actions.find(a => a.type === 'command');
    if (cmdAction) {
      return `Executed ${cmdAction.detail || 'command'}`;
    }
    const searchAction = actions.find(a => a.type === 'search');
    if (searchAction) {
      return `Searched ${searchAction.detail || 'web'}`;
    }
    return thoughtSummary;
  };

  // Synchronize messages changes into current session and localStorage
  useEffect(() => {
    setSessions(prev => {
      const idx = prev.findIndex(s => s.id === activeSessionId);
      if (idx === -1) return prev;
      const updated = [...prev];
      let newTitle = updated[idx].title;
      if (newTitle === 'New Conversation' || newTitle === 'ยินดีต้อนรับสู่ Sent-Code') {
        const firstUserMsg = messages.find(m => m.sender === 'user');
        if (firstUserMsg && firstUserMsg.text.trim()) {
          newTitle = firstUserMsg.text.trim().slice(0, 28) + (firstUserMsg.text.trim().length > 28 ? '...' : '');
        }
      }
      updated[idx] = {
        ...updated[idx],
        title: newTitle,
        updatedAt: Date.now(),
        messages: messages,
        projectFolder: projectFolder,
      };
      try {
        localStorage.setItem(SESSIONS_STORAGE_KEY, JSON.stringify(updated));
      } catch (e) {}
      return updated;
    });
  }, [messages, activeSessionId, projectFolder]);

  const loadGrokSessionsHistory = async (folder: string, skipMessagesForSessionId?: string) => {
    try {
      const grokSessions = await GetGrokSessions(folder);
      if (grokSessions && grokSessions.length > 0) {
        const converted: ChatSession[] = grokSessions.map(s => ({
          id: s.id,
          title: s.title,
          projectFolder: folder,
          updatedAt: s.updatedAt,
          messages: [],
        }));

        setSessions(prev => {
          const diskIds = new Set(converted.map(s => s.id));
          const localOnly = prev.filter(s => !diskIds.has(s.id));
          const merged = converted.map(cs => {
            const existing = prev.find(p => p.id === cs.id);
            if (existing && existing.messages && existing.messages.length > 0) {
              return { ...cs, messages: existing.messages };
            }
            return cs;
          });
          return [...localOnly, ...merged];
        });

        // Only auto-load messages for a session that was already the active one before
        // this function was called, AND only when it is not the session that just received a
        // fresh response (to avoid overwriting the just-rendered reply with stale disk data).
        const currentActive = localStorage.getItem(ACTIVE_SESSION_STORAGE_KEY);
        if (
          currentActive &&
          currentActive !== skipMessagesForSessionId
        ) {
          const activeTarget = converted.find(s => s.id === currentActive);
          if (activeTarget) {
            await loadMessagesForSession(folder, activeTarget.id);
          }
        }
      }

      // Check cache volume and prompt warning if large
      try {
        const stats = await GetCacheStats(folder);
        if (stats && stats.needsWarning) {
          setCacheWarning(stats);
        } else {
          setCacheWarning(null);
        }
      } catch (err) {}
    } catch (e) {
      console.error('Failed to load sessions:', e);
    }
  };

  const loadMessagesForSession = async (folder: string, sessionId: string) => {
    try {
      const msgs = await LoadGrokSessionMessages(folder, sessionId);
      if (msgs && msgs.length > 0) {
        const formatted: Message[] = msgs.map(m => ({
          id: m.id,
          sender: m.sender as 'user' | 'assistant',
          text: m.text,
          thought: m.thought,
          actions: m.actions,
          model: m.model || (m.sender === 'assistant' ? 'grok-auto' : undefined),
          timestamp: m.timestamp,
        }));
        setMessages(formatted);
        setSessions(prev => prev.map(s => s.id === sessionId ? { ...s, messages: formatted } : s));
      }
    } catch (e) {
      console.error('Failed to load messages for session:', e);
    }
  };

  const handleSwitchSession = async (sessionId: string) => {
    setActiveSessionId(sessionId);
    localStorage.setItem(ACTIVE_SESSION_STORAGE_KEY, sessionId);
    const targetSession = sessions.find(s => s.id === sessionId);
    if (targetSession && targetSession.messages && targetSession.messages.length > 0) {
      setMessages(targetSession.messages);
      return;
    }
    await loadMessagesForSession(projectFolder, sessionId);
  };

  const handleCreateNewSession = () => {
    const newId = `session_${Date.now()}`;
    const newSession: ChatSession = {
      id: newId,
      title: 'New Conversation',
      projectFolder: projectFolder,
      updatedAt: Date.now(),
      messages: [],
    };
    setSessions(prev => [newSession, ...prev]);
    setActiveSessionId(newId);
    setMessages([]);
    localStorage.setItem(ACTIVE_SESSION_STORAGE_KEY, newId);
    try {
      localStorage.setItem(SESSIONS_STORAGE_KEY, JSON.stringify([newSession, ...sessions]));
    } catch (e) {}
  };

  const handleDeleteSession = async (e: React.MouseEvent, sessionId: string) => {
    e.stopPropagation();
    try {
      await DeleteGrokSession(projectFolder, sessionId);
    } catch (err) {
      console.error('Failed to delete grok session from disk:', err);
    }
    if (sessions.length <= 1) {
      setMessages([]);
      return;
    }
    const filtered = sessions.filter(s => s.id !== sessionId);
    setSessions(filtered);
    try {
      localStorage.setItem(SESSIONS_STORAGE_KEY, JSON.stringify(filtered));
    } catch (e) {}
    if (activeSessionId === sessionId) {
      const nextSession = filtered[0];
      setActiveSessionId(nextSession.id);
      setMessages(nextSession.messages || []);
      localStorage.setItem(ACTIVE_SESSION_STORAGE_KEY, nextSession.id);
      loadMessagesForSession(projectFolder, nextSession.id);
    }
    try {
      const stats = await GetCacheStats(projectFolder);
      if (stats && stats.needsWarning) {
        setCacheWarning(stats);
      } else {
        setCacheWarning(null);
      }
    } catch (err) {}
  };

  const handleClearOldSessions = async () => {
    try {
      await ClearOldSessions(projectFolder, 10);
      setCacheWarning(null);
      await loadGrokSessionsHistory(projectFolder);
    } catch (err) {
      console.error('Failed to clear old sessions:', err);
    }
  };

  const formatRelativeTime = (timestamp: number) => {
    const diff = Math.floor((Date.now() - timestamp) / 1000);
    if (diff < 60) return 'now';
    if (diff < 3600) return `${Math.floor(diff / 60)}m`;
    if (diff < 86400) return `${Math.floor(diff / 3600)}h`;
    return `${Math.floor(diff / 86400)}d`;
  };

  const loadFiles = async (folder: string) => {
    try {
      const files = await ListProjectFiles(folder);
      setProjectFiles(files || []);
    } catch (e) {
      console.error(e);
    }
  };

  const updateQuota = async () => {
    try {
      const q = await CheckQuotaStatus();
      setTokensRemaining(q.tokensRemaining);
    } catch (e) {
      console.error(e);
    }
  };

  const handleSwitchWorkspace = async (targetPath: string) => {
    setIsWorkspaceDropdownOpen(false);
    setWorkspaceSearch('');
    if (targetPath === projectFolder) return;

    setProjectFolder(targetPath);
    try {
      localStorage.setItem('ah_code_current_project', targetPath);
    } catch (e) {}

    if (targetPath) {
      await SetCurrentFolder(targetPath);
      const branch = await GetProjectBranch(targetPath);
      setProjectBranch(branch || 'master');
      loadFiles(targetPath);
      loadGrokSessionsHistory(targetPath);
      loadGrokQuota(targetPath);

      setRecentWorkspaces(prev => {
        const baseName = targetPath.split('\\').pop() || targetPath.split('/').pop() || targetPath;
        const filtered = prev.filter(w => w.path.toLowerCase() !== targetPath.toLowerCase());
        return [{ path: targetPath, name: baseName, branch: branch || '' }, ...filtered];
      });
    } else {
      await SetCurrentFolder('');
      setProjectBranch('');
      setProjectFiles([]);
    }
  };

  const handleSelectFolder = async () => {
    try {
      const folder = await SelectProjectFolder();
      if (folder) {
        handleSwitchWorkspace(folder);
      }
    } catch (e) {
      console.error(e);
    }
  };

  const handleManualImageUpload = (e: React.ChangeEvent<HTMLInputElement>) => {
    const files = e.target.files;
    if (!files) return;
    for (let i = 0; i < files.length; i++) {
      const reader = new FileReader();
      reader.onload = (event) => {
        if (event.target?.result) {
          setAttachedImages(prev => [...prev, event.target!.result as string]);
        }
      };
      reader.readAsDataURL(files[i]);
    }
  };

  // overrideText lets a caller send a message without going through the (async) inputText state,
  // e.g. handing a build failure straight to the model.
  const handleSendMessage = async (overrideText?: string) => {
    const text = overrideText ?? inputText;
    if (!text.trim() && attachedImages.length === 0) return;

    const userMsg: Message = {
      id: Date.now().toString(),
      sender: 'user',
      text,
      images: [...attachedImages],
      timestamp: new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }),
    };

    setMessages(prev => [...prev, userMsg]);
    const sentText = text;
    const sentImages = [...attachedImages];
    liveStepsRef.current = [];
    setLiveSteps([]);
    setLiveThoughtText('');
    setIsLiveThoughtOpen(false);
    setInputText('');
    setAttachedImages([]);
    setLoading(true);

    if (sentImages.length > 0) {
      setActiveLoadingContext({
        type: 'image',
        text: `attached screenshot (${sentImages.length} image${sentImages.length > 1 ? 's' : ''})`,
      });
    } else {
      const found = projectFiles.find(
        f => !f.isDir && sentText.toLowerCase().includes(f.name.toLowerCase())
      );
      if (found) {
        const ext = found.name.split('.').pop() || '';
        const icon =
          ext === 'tsx' || ext === 'jsx' || ext === 'go' || ext === 'ts' || ext === 'js' || ext === 'py'
            ? 'code'
            : 'file';
        setActiveLoadingContext({
          type: 'file',
          text: `Exploring ${found.name}`,
          file: found.name,
          icon,
        });
      } else {
        setActiveLoadingContext({
          type: 'reasoning',
          text: 'Thinking...',
        });
      }
    }

    try {
      userCancelledRef.current = false;
      // Local models have no session on disk, so hand them the recent conversation as context
      const failed = ['gateway_error', 'quota_exceeded', 'cancelled', 'model_unavailable', 'error'];
      const history = messages
        .filter(m => m.text && !failed.includes(m.status || ''))
        .slice(-12)
        .map(m => ({ role: m.sender, content: m.text, model: m.sender === 'assistant' ? (m.model || '') : '' }));

      const res = await SendChatMessage(main.ChatRequest.createFrom({
        message: sentText,
        model: selectedModel,
        images: sentImages,
        projectPath: projectFolder,
        sessionId: activeSessionId.startsWith('session_') ? '' : activeSessionId,
        // Stable id for this conversation regardless of engine, unlike sessionId above (which is
        // blanked unless it's a real Grok session id). Keys the durable on-disk conversation log.
        logSessionId: activeSessionId,
        history,
      }));

      const recordedSteps = [...liveStepsRef.current];
      // Agent-run file reads (e.g. Claude Code's Read tool) are shown like Grok's: "Exploring N files" /
      // "Analyzed <file>". Init/model bookkeeping steps stay as plain status lines.
      const stepActions: AgentAction[] = recordedSteps.map(s => {
        const isRead = (s.icon === 'file' || s.icon === 'code') && /^(claude-|step-read)/.test(s.id);
        return {
          type: isRead ? 'read_file' : s.id.startsWith('step-tool') ? 'command' : s.id.startsWith('step-search') ? 'search' : s.id === 'step-vision' ? 'view_image' : 'status',
          label: isRead ? 'Analyzed' : s.title,
          detail: isRead ? (s.detail || '').split(/[\\/]/).pop() || s.title : s.detail,
          icon: s.icon,
        };
      });

      if (res.status === 'cancelled') {
        const assistantMsg: Message = {
          id: (Date.now() + 1).toString(),
          sender: 'assistant',
          text: '_Generation stopped by user._',
          thought: res.thought,
          actions: ((res as any).actions && (res as any).actions.length > 0)
            ? (res as any).actions
            : (stepActions.length > 0 ? stepActions : [{ type: 'status', label: 'Stopped', icon: '⏹' }]),
          status: 'cancelled',
          timestamp: new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }),
        };
        setMessages(prev => [...prev, assistantMsg]);
        return;
      }

      const finalActions = ((res as any).actions && (res as any).actions.length > 0)
        ? (res as any).actions
        : (stepActions.length > 0 ? stepActions : [{ type: 'status', label: 'Finished turn', icon: '✓' }]);

      const combinedThought = res.thought
        ? (liveThoughtText && !res.thought.includes(liveThoughtText) ? `${res.thought}\n\n${liveThoughtText}` : res.thought)
        : (liveThoughtText ? `Thought for ${activeThoughtElapsed}s\n\n${liveThoughtText}` : undefined);

      const assistantMsg: Message = {
        id: (Date.now() + 1).toString(),
        sender: 'assistant',
        text: res.reply,
        thought: combinedThought,
        actions: finalActions,
        commandRan: res.commandRan,
        status: res.status,
        model: selectedModel,
        diffStats: (res as any).diffStats,
        timestamp: new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }),
      };

      setMessages(prev => [...prev, assistantMsg]);
      setTokensRemaining(res.tokensLeft);
      // finalSessionId is whatever Grok assigned (may differ from activeSessionId on first turn)
      const finalSessionId = (res.sessionId && res.sessionId !== activeSessionId)
        ? res.sessionId
        : activeSessionId;
      if (res.sessionId && res.sessionId !== activeSessionId) {
        setActiveSessionId(res.sessionId);
        localStorage.setItem(ACTIVE_SESSION_STORAGE_KEY, res.sessionId);
      }
      if (res.thought) {
        setOpenThoughts(prev => ({ ...prev, [assistantMsg.id]: true }));
      }
      setTimeout(() => {
        // Pass finalSessionId so loadGrokSessionsHistory won't call loadMessagesForSession
        // for the session we just rendered — that would overwrite the fresh reply with stale disk data.
        loadGrokSessionsHistory(projectFolder, finalSessionId);
      }, 600);
    } catch (err: any) {
      if (userCancelledRef.current) {
        setMessages(prev => [
          ...prev,
          {
            id: (Date.now() + 1).toString(),
            sender: 'assistant',
            text: '_Generation stopped by user._',
            status: 'cancelled',
            actions: [{ type: 'status', label: 'Stopped', icon: '⏹' }],
            timestamp: new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }),
          }
        ]);
      } else {
        setMessages(prev => [
          ...prev,
          {
            id: (Date.now() + 1).toString(),
            sender: 'assistant',
            text: 'Service gateway unavailable (ERR_CONNECTION_REFUSED: 502). Please verify your network connectivity or subscription status.',
            status: 'gateway_error',
            timestamp: new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }),
          }
        ]);
      }
    } finally {
      userCancelledRef.current = false;
      setLoading(false);
    }
  };

  const handleStopChat = async () => {
    userCancelledRef.current = true;
    try {
      await StopActiveChat();
    } catch (e) {
      console.error('Stop active chat error:', e);
    } finally {
      setLoading(false);
    }
  };

  const handleRunBuild = async () => {
    if (!projectFolder) return;
    setLoading(true);
    try {
      const detected = await DetectBuildCommand(projectFolder);
      if (!detected.found) {
        setMessages(prev => [...prev, {
          id: Date.now().toString(),
          sender: 'assistant',
          thought: 'Analyzed project environment',
          text: "Couldn't recognize a build system in this folder (looked for package.json, go.mod, Cargo.toml, pom.xml/build.gradle, *.sln/*.csproj, requirements.txt/pyproject.toml). Ask the assistant to run a specific command instead.",
          actions: [{ type: 'status', label: 'No build system detected', icon: '⚠️' }],
          timestamp: new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }),
        }]);
        return;
      }

      const res = await RunBuildCommand(projectFolder, detected.command);
      const assistantMsg: Message = {
        id: Date.now().toString(),
        sender: 'assistant',
        thought: `Analyzed project environment (${detected.label})`,
        text: res.success
          ? `**Build succeeded** — ${detected.label}\n\nCommand: \`${res.command}\`\nExecution time: ${res.duration}`
          : `**Build failed** — ${detected.label}\n\nCommand: \`${res.command}\`\nExecution time: ${res.duration}`,
        commandRan: res,
        status: res.success ? undefined : 'build_failed',
        timestamp: new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }),
      };
      setMessages(prev => [...prev, assistantMsg]);
    } catch (e) {
      console.error(e);
    } finally {
      setLoading(false);
    }
  };

  // Hands the failed build's output to the current model and sends it as the next chat turn
  const handleAskAiToFixBuild = (res: main.CommandResult) => {
    const log = (res.stdout || res.stderr || '(no output)').slice(-4000);
    handleSendMessage(
      `The build command \`${res.command}\` failed (exit code ${res.exitCode}). Here is the output:\n\n\`\`\`\n${log}\n\`\`\`\n\nFind the cause and show the fix.`
    );
  };

  const handleRefill = async (amount: number) => {
    const newBal = await RefillTokens(amount);
    setTokensRemaining(newBal);
  };

  const handleGatewayLogin = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!loginEmail || !loginEmail.includes('@')) {
      setLoginError('Please enter a valid email address');
      return;
    }
    setLoginLoading(true);
    setLoginError('');
    try {
      const user = await LoginGateway(loginEmail);
      if (user) {
        setGatewayUser(user);
        setTokensRemaining(user.tokens);
        localStorage.setItem('sent_code_session_token', user.sessionToken);
        localStorage.setItem('sent_code_user_email', user.email);
        setIsAuthModalOpen(false);
      }
    } catch (err: any) {
      setLoginError(err?.message || 'Login failed. Please verify the web gateway is running.');
    } finally {
      setLoginLoading(false);
    }
  };

  const handleGatewayLogout = () => {
    setGatewayUser(null);
    localStorage.removeItem('sent_code_session_token');
    localStorage.removeItem('sent_code_user_email');
    setTokensRemaining(100);
    setIsAuthModalOpen(false);
  };

  const toggleOnline = async () => {
    const next = !isOnline;
    await SetOnlineStatus(next);
    setIsOnline(next);
  };

  const renderPromptBox = (isCentered: boolean) => {
    return (
      <div className={`w-full ${isCentered ? 'max-w-2xl' : 'max-w-4xl'} mx-auto relative z-30`}>
          <div className="bg-[#18191d] border border-[#2b2d35] rounded-2xl shadow-2xl p-3 flex flex-col gap-2.5 transition focus-within:border-cyan-500/50">
            {/* Top Pills Row: Workspace Selector & Git Branch */}
            <div className="flex items-center gap-2 px-1 mb-1">
              {/* Workspace Selector Pill */}
              <div className="relative">
                <div className="inline-flex items-center rounded-lg bg-[#22252c] hover:bg-[#292c35] border border-[#2f333e] text-xs text-gray-200 transition">
                  {projectFolder ? (
                    <>
                      <button
                        type="button"
                        onClick={(e) => {
                          e.stopPropagation();
                          handleSwitchWorkspace('');
                        }}
                        title="Work outside a project (No project)"
                        className="p-1.5 hover:text-rose-400 text-gray-400 transition cursor-pointer"
                      >
                        <X size={12} />
                      </button>
                      <button
                        type="button"
                        onClick={() => setIsWorkspaceDropdownOpen(prev => !prev)}
                        className="flex items-center gap-1.5 py-1 pr-2.5 text-xs font-medium cursor-pointer text-gray-200 hover:text-white"
                      >
                        <Folder size={13} className="text-cyan-400 shrink-0" />
                        <span className="truncate max-w-[150px]">
                          {projectFolder.split('\\').pop() || projectFolder.split('/').pop() || 'Sent-Code'}
                        </span>
                        <ChevronDown size={12} className="text-gray-400 shrink-0" />
                      </button>
                    </>
                  ) : (
                    <button
                      type="button"
                      onClick={() => setIsWorkspaceDropdownOpen(prev => !prev)}
                      className="flex items-center gap-1.5 px-2.5 py-1 text-xs font-medium text-gray-300 hover:text-white cursor-pointer"
                    >
                      <Folder size={13} className="text-gray-400 shrink-0" />
                      <span>No project</span>
                      <ChevronDown size={12} className="text-gray-400 shrink-0 ml-0.5" />
                    </button>
                  )}
                </div>

                {/* Workspace Switcher Dropdown */}
                {isWorkspaceDropdownOpen && (
                  <>
                    <div
                      className="fixed inset-0 z-[60] bg-transparent"
                      onClick={() => setIsWorkspaceDropdownOpen(false)}
                    />
                    <div className={`absolute ${isCentered ? 'top-full mt-1.5' : 'bottom-full mb-1.5'} left-0 w-80 bg-[#1b1d24] border border-[#2e313a] rounded-xl shadow-2xl p-1 z-[70] flex flex-col gap-0.5`}>
                      {/* Search Bar */}
                      <div className="p-1.5 border-b border-[#262832] mb-1">
                        <div className="flex items-center gap-2 px-2.5 py-1 rounded bg-[#131418] border border-[#262832] text-xs">
                          <Search size={13} className="text-gray-400 shrink-0" />
                          <input
                            type="text"
                            placeholder="Search workspaces"
                            value={workspaceSearch}
                            onChange={e => setWorkspaceSearch(e.target.value)}
                            className="w-full bg-transparent focus:outline-none text-xs text-gray-200 placeholder-gray-500"
                            autoFocus
                          />
                        </div>
                      </div>

                      {/* Workspaces List */}
                      <div className="max-h-48 overflow-y-auto space-y-0.5">
                        {filteredWorkspaces.map(ws => {
                          const isSelected = !!projectFolder && ws.path.toLowerCase() === projectFolder.toLowerCase();
                          return (
                            <button
                              key={ws.path}
                              type="button"
                              onClick={() => handleSwitchWorkspace(ws.path)}
                              className={`w-full flex items-center justify-between px-2.5 py-1.5 rounded-lg text-xs transition cursor-pointer text-left ${
                                isSelected
                                  ? 'bg-[#252832] text-white font-medium'
                                  : 'hover:bg-[#20222a] text-gray-300'
                              }`}
                            >
                              <div className="flex items-center gap-2 truncate min-w-0">
                                <Folder size={13} className={isSelected ? 'text-cyan-400 shrink-0' : 'text-gray-400 shrink-0'} />
                                <span className="truncate">{ws.name}</span>
                              </div>
                              {isSelected && <Check size={13} className="text-cyan-400 shrink-0 ml-1.5" />}
                            </button>
                          );
                        })}
                        {filteredWorkspaces.length === 0 && (
                          <div className="text-center py-2 text-gray-500 text-xs">No workspaces found</div>
                        )}
                      </div>

                      <div className="border-t border-[#262832] my-1" />

                      <button
                        type="button"
                        onClick={() => {
                          setIsWorkspaceDropdownOpen(false);
                          handleSelectFolder();
                        }}
                        className="w-full flex items-center gap-2 px-2.5 py-1.5 rounded-lg hover:bg-[#20222a] transition cursor-pointer text-left text-xs text-gray-300"
                      >
                        <FolderOpen size={13} className="text-gray-400" />
                        <span>Open folder</span>
                      </button>
                      <button
                        type="button"
                        onClick={() => {
                          setIsWorkspaceDropdownOpen(false);
                          alert('Remote connection configured for local development.');
                        }}
                        className="w-full flex items-center gap-2 px-2.5 py-1.5 rounded-lg hover:bg-[#20222a] transition cursor-pointer text-left text-xs text-gray-400"
                      >
                        <Cloud size={13} className="text-gray-400" />
                        <span>Remote connection</span>
                      </button>
                      <button
                        type="button"
                        onClick={() => {
                          handleSwitchWorkspace('');
                        }}
                        className={`w-full flex items-center justify-between px-2.5 py-1.5 rounded-lg hover:bg-[#20222a] transition cursor-pointer text-left text-xs ${
                          !projectFolder ? 'bg-[#252832] text-white font-medium' : 'text-gray-300'
                        }`}
                      >
                        <div className="flex items-center gap-2">
                          <Folder size={13} className={!projectFolder ? 'text-cyan-400' : 'text-gray-400'} />
                          <span>No project (Work outside a project)</span>
                        </div>
                        {!projectFolder && <Check size={13} className="text-cyan-400 shrink-0 ml-1.5" />}
                      </button>
                    </div>
                  </>
                )}
              </div>

              {/* Git Branch Pill */}
              {projectFolder && projectBranch && (
                <div className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-lg bg-[#22252c] border border-[#2f333e] text-xs text-gray-300 font-mono">
                  <GitBranch size={12} className="text-gray-400" />
                  <span>{projectBranch}</span>
                </div>
              )}
            </div>
            {/* Attached Images Chips */}
            {attachedImages.length > 0 && (
              <div className="flex flex-wrap gap-2 pt-1 px-1">
                {attachedImages.map((img, i) => (
                  <div key={i} className="relative group rounded-xl overflow-hidden border border-[#3b3e48] hover:border-cyan-400/80 transition cursor-pointer">
                    <img
                      src={img}
                      alt="attached"
                      className="h-16 w-16 object-cover"
                      onClick={() => setPreviewImage(img)}
                      title="คลิกเพื่อดูรูปภาพ"
                    />
                    <button
                      onClick={(e) => {
                        e.stopPropagation();
                        setAttachedImages(prev => prev.filter((_, idx) => idx !== i));
                      }}
                      className="absolute top-1 right-1 p-0.5 bg-black/70 hover:bg-black text-white rounded-full transition cursor-pointer"
                      title="ลบรูปภาพ"
                    >
                      <X size={11} />
                    </button>
                  </div>
                ))}
              </div>
            )}

            {/* Input Textarea */}
            <textarea
              value={inputText}
              onChange={(e) => setInputText(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && !e.shiftKey) {
                  e.preventDefault();
                  handleSendMessage();
                }
              }}
              placeholder="Ask AH-Code to vibe code, run builds, or press Ctrl+V to paste screenshots..."
              rows={2}
              className="w-full bg-transparent resize-none text-sm text-gray-100 placeholder-gray-500 focus:outline-none px-1 leading-relaxed"
            />

            {/* Bottom Controls Bar */}
            <div className="flex items-center justify-between pt-1 text-xs">
              {/* Left Buttons: Attach + Model Selector */}
              <div className="flex items-center gap-2">
                {/* File / Image attachment button */}
                <input
                  type="file"
                  ref={fileInputRef}
                  onChange={handleManualImageUpload}
                  accept="image/*"
                  multiple
                  className="hidden"
                />
                <button
                  onClick={() => fileInputRef.current?.click()}
                  className="p-1.5 rounded-lg hover:bg-[#252830] text-gray-400 hover:text-gray-200 transition cursor-pointer"
                  title="Attach screenshot or mockup"
                >
                  <Plus size={16} />
                </button>

                  {/* Model Selector Dropdown */}
                <div className="relative">
                  <button
                    onClick={() => {
                      if (isModelDropdownOpen) {
                        setIsModelDropdownOpen(false);
                        setIsGrokSubmenuOpen(false);
                        setIsQwenSubmenuOpen(false);
                        setIsClaudeSubmenuOpen(false);
                      } else {
                        setIsModelDropdownOpen(true);
                        refreshModels();
                      }
                    }}
                    className="flex items-center gap-1.5 px-2.5 py-1 rounded-lg bg-[#22242a] hover:bg-[#282a32] border border-[#2f323a] text-gray-300 font-medium transition cursor-pointer text-xs"
                  >
                    {!selectedModel.startsWith('grok-') ? (
                      <>
                        {models.find(m => m.id === selectedModel)?.kind === 'cli'
                          ? <Terminal size={13} className={selectedModel.startsWith('claude-code') ? 'text-orange-400' : 'text-emerald-400'} />
                          : <Cpu size={13} className="text-emerald-400" />}
                        <span>
                          {(() => {
                            const activeQwenTier = qwenTiers.find(t => isTierActive(t.id));
                            if (activeQwenTier) return `Qwen-Coder ${activeQwenTier.label}`;
                            const activeClaudeTier = claudeTiers.find(t => isTierActive(t.id));
                            if (activeClaudeTier) return `Claude Code ${activeClaudeTier.label}`;
                            return models.find(m => m.id === selectedModel)?.name || 'Qwen-Coder';
                          })()}
                        </span>
                      </>
                    ) : grokTier === 'NONE' ? (
                      <>
                        <Sparkles size={13} className="text-cyan-400" />
                        <span>Grok</span>
                      </>
                    ) : (
                      <>
                        {selectedModel === 'grok-fast' && <Zap size={13} className="text-amber-400" />}
                        {selectedModel === 'grok-auto' && <Sparkles size={13} className="text-cyan-400" />}
                        {selectedModel === 'grok-expert' && <Brain size={13} className="text-purple-400" />}
                        {selectedModel === 'grok-heavy' && <Flame size={13} className="text-rose-400" />}
                        <span>
                          {selectedModel === 'grok-fast' && 'Grok Fast'}
                          {selectedModel === 'grok-auto' && 'Grok Auto'}
                          {selectedModel === 'grok-expert' && 'Grok Expert'}
                          {selectedModel === 'grok-heavy' && 'Grok Heavy'}
                        </span>
                      </>
                    )}
                    <ChevronDown size={12} className="text-gray-400 ml-0.5" />
                  </button>

                  {/* Dropdown Menu */}
                  {isModelDropdownOpen && (
                    <>
                      <div
                        className="fixed inset-0 z-40 bg-transparent"
                        onClick={() => setIsModelDropdownOpen(false)}
                      />
                      <div
                        className={`absolute ${isCentered ? "top-full mt-1.5" : "bottom-full mb-1.5"} left-0 w-80 bg-[#16171c] border border-[#2b2e38] rounded-xl shadow-2xl p-2 flex flex-col gap-1.5 z-50`}
                      >
                        {/* Header: Antigravity style */}
                        <div className="px-2 py-1 text-xs font-semibold text-gray-300 border-b border-[#252832] pb-1.5 flex items-center justify-between">
                          <span>Select a model</span>
                          <span className="text-[10px] text-gray-500 font-mono">Agent Mode</span>
                        </div>

                        {/* Model Items List */}
                        <div className="flex flex-col gap-1 pt-0.5 max-h-64 overflow-y-auto custom-scrollbar">
                          {/* 1. Grok */}
                          {grokTier === 'NONE' ? (
                            <button
                              type="button"
                              onClick={() => {
                                handleTriggerLogin();
                                setIsModelDropdownOpen(false);
                              }}
                              className="flex items-center justify-between p-2 rounded-lg text-left transition cursor-pointer hover:bg-[#20222a] text-gray-300 group"
                            >
                              <div className="flex items-center gap-2.5">
                                <div className="p-1.5 rounded-lg bg-cyan-950/50 border border-cyan-800/40 text-cyan-400">
                                  <Sparkles size={14} />
                                </div>
                                <div className="flex flex-col">
                                  <div className="flex items-center gap-1.5">
                                    <span className="font-semibold text-xs text-white">Grok Auto</span>
                                    <span className="text-[9px] px-1.5 py-0.2 rounded bg-cyan-500/20 text-cyan-300 font-semibold border border-cyan-500/30">
                                      Recommended
                                    </span>
                                  </div>
                                  <span className="text-[10px] text-gray-400">Click to connect SuperGrok account</span>
                                </div>
                              </div>
                              <span className="text-[10px] px-2 py-0.5 rounded bg-cyan-500/20 text-cyan-300 font-medium border border-cyan-500/30 group-hover:bg-cyan-500 group-hover:text-gray-950 transition">
                                Login
                              </span>
                            </button>
                          ) : (
                            <button
                              type="button"
                              onClick={() => {
                                const effortIdx = getEffortIndex(selectedModel);
                                const grokIds = ['grok-fast', 'grok-auto', 'grok-expert', 'grok-heavy'];
                                setSelectedModel(grokIds[effortIdx]);
                                setIsModelDropdownOpen(false);
                              }}
                              className={`flex items-center justify-between p-2 rounded-lg text-left transition cursor-pointer ${
                                selectedModel.startsWith('grok-')
                                  ? 'bg-cyan-950/60 border border-cyan-800/40 text-cyan-200'
                                  : 'hover:bg-[#20222a] text-gray-300'
                              }`}
                            >
                              <div className="flex items-center gap-2.5 min-w-0">
                                <div className={`p-1.5 rounded-lg ${selectedModel.startsWith('grok-') ? 'bg-cyan-900/50 text-cyan-300' : 'bg-[#20222a] text-cyan-400'}`}>
                                  <Sparkles size={14} />
                                </div>
                                <div className="flex flex-col min-w-0">
                                  <div className="flex items-center gap-1.5">
                                    <span className="font-semibold text-xs text-white">Grok</span>
                                    <span className="text-[9px] px-1.5 py-0.2 rounded bg-cyan-500/20 text-cyan-300 font-semibold border border-cyan-500/30">
                                      Default (recommended)
                                    </span>
                                  </div>
                                  <span className="text-[10px] text-gray-400 truncate">
                                    Cloud agent with vision & web search
                                  </span>
                                </div>
                              </div>
                              {selectedModel.startsWith('grok-') && (
                                <Check size={14} className="text-cyan-400 shrink-0 ml-1.5" />
                              )}
                            </button>
                          )}

                          {/* 2. Claude Code */}
                          <button
                            type="button"
                            onClick={() => {
                              const effortIdx = getEffortIndex(selectedModel);
                              const claudeIds = ['claude-code-low', 'claude-code-medium', 'claude-code-high', 'claude-code-ultra'];
                              setSelectedModel(claudeIds[effortIdx]);
                              setIsModelDropdownOpen(false);
                            }}
                            className={`flex items-center justify-between p-2 rounded-lg text-left transition cursor-pointer ${
                              selectedModel.startsWith('claude-code')
                                ? 'bg-orange-950/50 border border-orange-800/40 text-orange-200'
                                : 'hover:bg-[#20222a] text-gray-300'
                            }`}
                          >
                            <div className="flex items-center gap-2.5 min-w-0">
                              <div className={`p-1.5 rounded-lg ${selectedModel.startsWith('claude-code') ? 'bg-orange-900/50 text-orange-300' : 'bg-[#20222a] text-orange-400'}`}>
                                <Terminal size={14} />
                              </div>
                              <div className="flex flex-col min-w-0">
                                <div className="flex items-center gap-1.5">
                                  <span className="font-semibold text-xs text-white">Claude Code</span>
                                  <span className="text-[9px] px-1.5 py-0.2 rounded bg-orange-500/20 text-orange-300 font-semibold border border-orange-500/30">
                                    CLI Agent
                                  </span>
                                </div>
                                <span className="text-[10px] text-gray-400 truncate">
                                  Anthropic terminal agent with local execution
                                </span>
                              </div>
                            </div>
                            {selectedModel.startsWith('claude-code') && (
                              <Check size={14} className="text-orange-400 shrink-0 ml-1.5" />
                            )}
                          </button>

                          {/* 3. Qwen-Coder (Local or Cloud GPU) */}
                          <button
                            type="button"
                            onClick={() => {
                              if (remoteOllamaStatus.connected) {
                                setSelectedModel('qwen-coder-32b');
                              } else {
                                const effortIdx = getEffortIndex(selectedModel);
                                const qwenIds = ['qwen-coder-7b', 'qwen-coder-7b', 'qwen-coder-14b', 'qwen-coder-32b'];
                                const targetId = qwenIds[effortIdx];
                                if (isTierInstalled(targetId)) {
                                  setSelectedModel(targetId);
                                } else {
                                  setSelectedModel('qwen-coder-7b');
                                }
                              }
                              setIsModelDropdownOpen(false);
                            }}
                            className={`flex items-center justify-between p-2 rounded-lg text-left transition cursor-pointer ${
                              !selectedModel.startsWith('grok-') && (selectedModel.includes('qwen') || selectedModel.includes('sent-code'))
                                ? 'bg-emerald-950/50 border border-emerald-800/40 text-emerald-200'
                                : 'hover:bg-[#20222a] text-gray-300'
                            }`}
                          >
                            <div className="flex items-center gap-2.5 min-w-0">
                              <div className={`p-1.5 rounded-lg ${!selectedModel.startsWith('grok-') && (selectedModel.includes('qwen') || selectedModel.includes('sent-code')) ? 'bg-emerald-900/50 text-emerald-300' : 'bg-[#20222a] text-emerald-400'}`}>
                                <Cpu size={14} />
                              </div>
                              <div className="flex flex-col min-w-0">
                                <div className="flex items-center gap-1.5">
                                  <span className="font-semibold text-xs text-white">Qwen-Coder</span>
                                  <span className={`text-[9px] px-1.5 py-0.2 rounded font-semibold border ${
                                    remoteOllamaStatus.connected
                                      ? 'bg-cyan-950 text-cyan-300 border-cyan-700/50'
                                      : 'bg-emerald-500/20 text-emerald-300 border-emerald-500/30'
                                  }`}>
                                    {remoteOllamaStatus.connected ? 'Cloud GPU (A100)' : 'Local Engine'}
                                  </span>
                                </div>
                                <span className="text-[10px] text-gray-400 truncate">
                                  {remoteOllamaStatus.connected
                                    ? 'Connected to Cloud GPU • Qwen 2.5 Coder 32B'
                                    : '100% private offline model on your GPU'}
                                </span>
                              </div>
                            </div>
                            {!selectedModel.startsWith('grok-') && (selectedModel.includes('qwen') || selectedModel.includes('sent-code')) && (
                              <Check size={14} className="text-emerald-400 shrink-0 ml-1.5" />
                            )}
                          </button>

                          {/* Custom models from registry if any */}
                          {models
                            .filter(m => ![
                              'sent-code-3b', 'qwen-coder-3b', 'qwen-coder', 'qwen-coder-7b', 'qwen-coder-14b', 'qwen-coder-32b',
                              'claude-code-low', 'claude-code-medium', 'claude-code-high', 'claude-code-ultra',
                              'qwen-vl-3b', 'moondream-vision',
                            ].includes(m.id))
                            .map(m => (
                              <button
                                key={m.id}
                                type="button"
                                onClick={() => {
                                  if (m.installed) {
                                    setSelectedModel(m.id);
                                    setIsModelDropdownOpen(false);
                                  }
                                }}
                                className={`flex items-center justify-between p-2 rounded-lg text-left transition cursor-pointer ${
                                  selectedModel === m.id
                                    ? 'bg-[#252832] text-white border border-[#3c404f]'
                                    : 'hover:bg-[#20222a] text-gray-300'
                                }`}
                              >
                                <div className="flex items-center gap-2.5 truncate">
                                  <Cpu size={14} className="text-gray-400 shrink-0" />
                                  <span className="text-xs font-medium truncate">{m.name}</span>
                                </div>
                                {selectedModel === m.id && <Check size={14} className="text-cyan-400 shrink-0 ml-1.5" />}
                              </button>
                            ))}
                        </div>

                        {/* Antigravity Effort Slider Footer */}
                        <div className="pt-2 px-2 pb-1 border-t border-[#252832] mt-0.5 flex flex-col gap-2">
                          <div className="flex items-center justify-between text-xs">
                            <span className="text-gray-400 font-medium">
                              {!selectedModel.startsWith('grok-') && (selectedModel.includes('qwen') || selectedModel.includes('sent-code'))
                                ? 'Model Size / Effort'
                                : 'Effort'}
                            </span>
                            <span className="text-cyan-300 font-semibold text-[11px] font-mono">
                              {(() => {
                                if (!selectedModel.startsWith('grok-') && (selectedModel.includes('qwen') || selectedModel.includes('sent-code'))) {
                                  const qwenTitles = ['Low (7B)', 'Medium (7B)', 'High (14B)', 'Ultra (32B)'];
                                  return qwenTitles[effortIndex] || effortLabels[effortIndex];
                                }
                                return effortLabels[effortIndex];
                              })()}
                            </span>
                          </div>
                          
                          {/* 4-dot segmented track matching Antigravity */}
                          <div className="flex items-center justify-between px-2 py-1.5 relative bg-[#121317] rounded-lg border border-[#22242c]">
                            <div className="absolute left-4 right-4 top-1/2 -translate-y-1/2 h-0.5 bg-[#262832]" />
                            {effortLabels.map((label, idx) => {
                              const active = effortIndex === idx;
                              const isQwen = !selectedModel.startsWith('grok-') && (selectedModel.includes('qwen') || selectedModel.includes('sent-code'));
                              const qwenSub = idx === 0 ? '7B' : idx === 1 ? '7B' : idx === 2 ? '14B' : '32B';
                              return (
                                <button
                                  key={label}
                                  type="button"
                                  onClick={() => handleSetEffort(idx)}
                                  className="relative z-10 flex flex-col items-center group cursor-pointer focus:outline-none py-0.5 px-1"
                                  title={isQwen ? `Qwen-Coder ${qwenSub} (${label})` : `Reasoning Effort: ${label}`}
                                >
                                  <div className={`w-3.5 h-3.5 rounded-full flex items-center justify-center transition-all ${
                                    active
                                      ? 'bg-cyan-400 ring-4 ring-cyan-500/25 scale-110'
                                      : 'bg-[#1e2027] border border-[#363a48] group-hover:border-gray-300'
                                  }`}>
                                    {active && <div className="w-1.5 h-1.5 bg-gray-950 rounded-full" />}
                                  </div>
                                  <span className={`text-[10px] mt-1 transition ${
                                    active ? 'text-cyan-300 font-bold' : 'text-gray-500 group-hover:text-gray-300'
                                  }`}>
                                    {isQwen ? qwenSub : label}
                                  </span>
                                </button>
                              );
                            })}
                          </div>
                        </div>
                      </div>
                    </>
                  )}
                </div>
              </div>

              {/* Right Send / Stop Button */}
              <div className="flex items-center gap-2">
                {loading ? (
                  <button
                    onClick={handleStopChat}
                    title="Stop generation"
                    className="w-8 h-8 rounded-full bg-rose-500/20 hover:bg-rose-500/30 text-rose-400 border border-rose-500/30 flex items-center justify-center transition cursor-pointer shadow-lg shadow-rose-500/10 group"
                  >
                    <Square size={12} className="fill-current group-hover:scale-110 transition-transform" />
                  </button>
                ) : (
                  <button
                    onClick={() => handleSendMessage()}
                    disabled={!inputText.trim() && attachedImages.length === 0}
                    title="Send message"
                    className="w-8 h-8 rounded-full bg-cyan-500 hover:bg-cyan-400 disabled:opacity-40 disabled:hover:bg-cyan-500 text-gray-950 flex items-center justify-center transition cursor-pointer shadow-lg shadow-cyan-500/20"
                  >
                    <ArrowUp size={16} strokeWidth={2.5} />
                  </button>
                )}
              </div>
            </div>
          </div>
        </div>
    );
  };

  return (
    <div className="flex flex-col h-screen w-screen bg-[#121316] text-[#e3e5e8] font-sans overflow-hidden">
      {/* 1. UNIFIED FRAMELESS TITLEBAR (Antigravity & VS Code Style) */}
      <header
        className="wails-drag h-10 bg-[#15161a] border-b border-[#24262d] flex items-center justify-between px-3 shrink-0 select-none z-50"
        onDoubleClick={() => WindowToggleMaximise()}
      >
        {/* Left: Brand, Sidebar Toggle, History Navigation */}
        <div className="flex items-center gap-2.5 wails-no-drag">
          <div className="flex items-center gap-2">
            <div className="w-5 h-5 rounded-md bg-gradient-to-tr from-cyan-500 to-blue-600 flex items-center justify-center font-bold text-white shadow-xs text-[10px] tracking-tighter">
              AH
            </div>
            <span className="font-semibold text-xs text-white tracking-wide">
              AH-Code
            </span>
            <span className="text-[9px] uppercase font-mono px-1 py-0.2 rounded bg-cyan-950/70 text-cyan-400 border border-cyan-800/40">
              v1.0
            </span>
          </div>

          <div className="w-[1px] h-3.5 bg-[#262832] mx-1" />

          {/* Sidebar Toggle */}
          <button
            onClick={() => setIsSidebarOpen(prev => !prev)}
            className="p-1 rounded-md hover:bg-[#20222a] hover:text-gray-200 text-gray-400 transition cursor-pointer"
            title={isSidebarOpen ? "Collapse Sidebar" : "Expand Sidebar"}
          >
            <PanelLeft size={14} />
          </button>

          {/* History navigation */}
          <div className="flex items-center text-gray-500">
            <button
              type="button"
              onClick={() => {
                const idx = sessions.findIndex(s => s.id === activeSessionId);
                if (idx < sessions.length - 1) handleSwitchSession(sessions[idx + 1].id);
              }}
              disabled={sessions.findIndex(s => s.id === activeSessionId) >= sessions.length - 1}
              className="p-1 rounded hover:bg-[#20222a] hover:text-gray-200 disabled:opacity-30 disabled:hover:bg-transparent transition cursor-pointer"
              title="Previous Session"
            >
              <ChevronLeft size={14} />
            </button>
            <button
              type="button"
              onClick={() => {
                const idx = sessions.findIndex(s => s.id === activeSessionId);
                if (idx > 0) handleSwitchSession(sessions[idx - 1].id);
              }}
              disabled={sessions.findIndex(s => s.id === activeSessionId) <= 0}
              className="p-1 rounded hover:bg-[#20222a] hover:text-gray-200 disabled:opacity-30 disabled:hover:bg-transparent transition cursor-pointer"
              title="Next Session"
            >
              <ChevronRight size={14} />
            </button>
          </div>
        </div>

        {/* Center: Dynamic Project & Session Breadcrumb (Draggable) */}
        <div className="flex items-center gap-2 text-xs truncate max-w-md mx-auto pointer-events-none">
          <span className="text-gray-400 font-medium truncate">
            {projectFolder ? (projectFolder.split('\\').pop() || projectFolder.split('/').pop() || 'Sent-Code') : 'No project'}
          </span>
          <span className="text-gray-600 font-normal">/</span>
          <span className="text-gray-300 truncate">
            {sessions.find(s => s.id === activeSessionId)?.title || 'New Conversation'}
          </span>
        </div>

        {/* Right: Actions, Settings & Window Controls */}
        <div className="flex items-center gap-2 wails-no-drag">
          <button
            onClick={handleRunBuild}
            className="flex items-center gap-1.5 px-2.5 py-0.5 rounded-full bg-[#1e2129] hover:bg-[#282d38] border border-[#2d323f] text-[11px] font-medium text-gray-300 hover:text-white transition cursor-pointer shadow-xs"
            title="Test Project Build"
          >
            <Play size={10} className="text-emerald-400 fill-emerald-400/20" />
            <span>Test Build</span>
          </button>
          <button
            onClick={handleSelectFolder}
            className="flex items-center gap-1.5 px-2.5 py-0.5 rounded-full bg-[#1e2129] hover:bg-[#282d38] border border-[#2d323f] text-[11px] font-medium text-gray-300 hover:text-white transition cursor-pointer shadow-xs"
            title="Change Project Folder"
          >
            <FolderOpen size={10} className="text-cyan-400" />
            <span>Change Project</span>
          </button>

          <button
            onClick={() => setIsSettingsOpen(true)}
            className="p-1 rounded-md hover:bg-[#20222a] text-gray-400 hover:text-gray-200 transition cursor-pointer"
            title="Settings"
          >
            <Settings size={13} />
          </button>

          <div className="w-[1px] h-3.5 bg-[#262832] mx-0.5" />

          {/* Windows Window Controls */}
          <div className="flex items-center -mr-1">
            <button
              onClick={() => WindowMinimise()}
              className="w-8 h-7 flex items-center justify-center text-gray-400 hover:text-white hover:bg-[#242730] transition cursor-pointer"
              title="Minimize"
            >
              <Minus size={13} />
            </button>
            <button
              onClick={() => WindowToggleMaximise()}
              className="w-8 h-7 flex items-center justify-center text-gray-400 hover:text-white hover:bg-[#242730] transition cursor-pointer"
              title="Maximize / Restore"
            >
              <Square size={11} />
            </button>
            <button
              onClick={() => Quit()}
              className="w-8 h-7 flex items-center justify-center text-gray-400 hover:text-white hover:bg-rose-600 transition cursor-pointer"
              title="Close"
            >
              <X size={13} />
            </button>
          </div>
        </div>
      </header>

      {/* 2. BODY CONTENT (Sidebar + Chat Area) */}
      <div className="flex-1 flex overflow-hidden">
        {/* LEFT SIDEBAR */}
        <aside className={`${isSidebarOpen ? 'w-72' : 'w-0 hidden'} bg-[#18191d] border-r border-[#24262d] flex flex-col h-full shrink-0 select-none transition-all duration-150`}>
          {/* Top items & full-height session list */}
          <div className="p-3 flex-1 min-h-0 flex flex-col gap-3 overflow-hidden">
            {/* New Conversation Button */}
            <button
              onClick={handleCreateNewSession}
              className="w-full flex items-center gap-2 px-3 py-2 rounded-lg bg-[#202228] hover:bg-[#282a32] border border-[#2d3039] text-sm font-medium transition cursor-pointer text-gray-200 shrink-0"
            >
              <Plus size={16} className="text-cyan-400" />
              <span>New Conversation</span>
            </button>

          {/* Quick links */}
          <div className="flex flex-col gap-0.5 text-xs text-gray-400 font-medium shrink-0">
            <button className="flex items-center gap-2.5 px-3 py-1.5 rounded-md hover:bg-[#202228] hover:text-gray-200 transition text-left cursor-pointer">
              <History size={14} />
              <span>Conversation History</span>
            </button>
            <button className="flex items-center gap-2.5 px-3 py-1.5 rounded-md hover:bg-[#202228] hover:text-gray-200 transition text-left cursor-pointer">
              <Clock size={14} />
              <span>Scheduled Tasks</span>
            </button>
          </div>

          {/* Projects Tree Section - Fills all available vertical space */}
          <div className="pt-2 border-t border-[#24262d] flex-1 min-h-0 flex flex-col gap-1.5">
            <div className="flex items-center justify-between px-2 text-[11px] font-semibold uppercase tracking-wider text-gray-400 shrink-0">
              <span>Projects & Sessions</span>
              <button
                onClick={handleSelectFolder}
                className="hover:text-cyan-400 transition cursor-pointer p-0.5 rounded hover:bg-[#22242a]"
                title="Open Folder"
              >
                <FolderOpen size={13} />
              </button>
            </div>

            {/* Active Project Card */}
            <div className="bg-[#202228]/80 border border-[#2d3039] rounded-lg p-2.5 flex flex-col gap-0.5 shrink-0">
              <div className="flex items-center gap-2 text-xs font-medium text-white truncate">
                <Folder size={14} className={projectFolder ? "text-cyan-400 shrink-0" : "text-gray-400 shrink-0"} />
                <span className="truncate">{projectFolder ? (projectFolder.split('\\').pop() || projectFolder.split('/').pop() || 'Sent-Code') : 'No project'}</span>
              </div>
              <div className="text-[11px] text-gray-400 truncate font-mono">
                {projectFolder || 'Working outside a project'}
              </div>
            </div>

            {/* Cache & Session Warning Banner (Shown only when volume affects performance) */}
            {cacheWarning && cacheWarning.needsWarning && (
              <div className="bg-amber-950/40 border border-amber-500/30 rounded-lg p-2.5 flex flex-col gap-1.5 text-xs text-amber-200 shrink-0">
                <div className="flex items-start justify-between gap-1.5">
                  <div className="flex items-center gap-1.5 font-medium text-amber-300">
                    <AlertTriangle size={13} className="shrink-0 text-amber-400" />
                    <span>ประวัติแชทเริ่มเยอะ</span>
                  </div>
                  <button
                    onClick={() => setCacheWarning(null)}
                    className="text-amber-400/60 hover:text-amber-200 transition cursor-pointer p-0.5"
                    title="ซ่อนการแจ้งเตือน"
                  >
                    <X size={12} />
                  </button>
                </div>
                <p className="text-[10.5px] text-amber-200/80 leading-snug">
                  {cacheWarning.warningMessage}
                </p>
                <button
                  onClick={handleClearOldSessions}
                  className="w-full mt-0.5 py-1 px-2 rounded bg-amber-500/20 hover:bg-amber-500/30 text-amber-300 border border-amber-500/40 text-[10.5px] font-medium transition cursor-pointer flex items-center justify-center gap-1.5"
                >
                  <Trash2 size={11} />
                  <span>ล้างประวัติเก่า (เก็บล่าสุด 10 รายการ)</span>
                </button>
              </div>
            )}

            {/* Conversation sessions - Fills full remaining height */}
            <div className="flex-1 min-h-0 flex flex-col gap-1 mt-1 overflow-y-auto pr-1">
              {sessions.map(s => {
                const isActive = s.id === activeSessionId;
                return (
                  <div
                    key={s.id}
                    onClick={() => handleSwitchSession(s.id)}
                    title={s.title || 'Untitled Session'}
                    className={`group flex items-center justify-between px-2.5 py-2 rounded-lg text-xs font-medium cursor-pointer transition ${
                      isActive
                        ? 'bg-[#252830] text-cyan-300 border border-cyan-500/20 shadow-xs'
                        : 'text-gray-400 hover:text-gray-200 hover:bg-[#202228]'
                    }`}
                  >
                    <div className="flex items-center gap-2 truncate pr-1 flex-1 min-w-0">
                      {isActive && <span className="w-1.5 h-1.5 rounded-full bg-cyan-400 shrink-0 shadow-xs shadow-cyan-400"></span>}
                      <span className="truncate leading-relaxed">{s.title || 'Untitled Session'}</span>
                    </div>
                    <div className="flex items-center gap-1 shrink-0 ml-1.5">
                      <span className="text-[10px] text-gray-500 group-hover:hidden font-mono">
                        {formatRelativeTime(s.updatedAt)}
                      </span>
                      <button
                        onClick={(e) => handleDeleteSession(e, s.id)}
                        className="hidden group-hover:block p-0.5 text-gray-500 hover:text-rose-400 rounded transition cursor-pointer"
                        title="Delete session"
                      >
                        <Trash2 size={12} />
                      </button>
                    </div>
                  </div>
                );
              })}
            </div>

            {/* Project files count */}
            {projectFiles.length > 0 && (
              <div className="pt-2 border-t border-[#24262d]/60 px-2 py-1 text-[11px] text-gray-400 flex items-center justify-between shrink-0">
                <span>Indexed files</span>
                <span className="font-mono text-gray-400">{projectFiles.length} files</span>
              </div>
            )}
          </div>
        </div>

        {/* Bottom items: User Profile & Settings */}
        <div className="p-3 border-t border-[#24262d] flex flex-col gap-2 shrink-0 bg-[#15161a]">
          {/* User Profile Card (Click to open login/account modal) */}
          <button
            onClick={() => setIsAuthModalOpen(true)}
            className="w-full flex items-center justify-between p-2 rounded-xl bg-[#1c1e24] hover:bg-[#23262e] border border-[#2b2e38] transition cursor-pointer text-left group"
            title="Manage account & token quota"
          >
            <div className="flex items-center gap-2.5 min-w-0">
              <div className="w-8 h-8 rounded-lg bg-gradient-to-tr from-cyan-500 to-emerald-400 text-gray-950 font-bold text-xs flex items-center justify-center shrink-0 shadow-sm">
                {gatewayUser ? (gatewayUser.initials || 'U') : <User size={15} />}
              </div>
              <div className="min-w-0">
                <div className="text-xs font-semibold text-white truncate">
                  {gatewayUser ? gatewayUser.name : 'Sign In'}
                </div>
                <div className="text-[10px] text-gray-400 truncate">
                  {gatewayUser ? gatewayUser.email : 'Connect Gateway'}
                </div>
              </div>
            </div>
            <div className="flex flex-col items-end shrink-0">
              <span className={`px-1.5 py-0.5 rounded text-[9px] font-bold uppercase ${
                gatewayUser?.plan === 'unlimited' ? 'bg-amber-950 text-amber-300 border border-amber-800/40' : 'bg-cyan-950 text-cyan-300 border border-cyan-800/40'
              }`}>
                {gatewayUser?.plan || 'Free'}
              </span>
              <span className="text-[10px] font-mono text-cyan-300 font-semibold mt-0.5">
                {tokensRemaining} tk
              </span>
            </div>
          </button>

          {/* Online Gateway Status Indicator & Settings Button */}
          <div className="flex items-center justify-between px-1 text-[11px] text-gray-400">
            <span className="flex items-center gap-1.5 text-[10px]">
              <span className={`w-1.5 h-1.5 rounded-full ${isOnline && gateway?.online ? 'bg-emerald-400 animate-pulse' : 'bg-rose-500'}`}></span>
              {gateway?.online ? 'Gateway Online' : 'Gateway Offline'}
            </span>
            <button
              onClick={() => setIsSettingsOpen(true)}
              className="flex items-center gap-1.5 px-2 py-1 rounded hover:bg-[#22242a] text-xs font-medium text-gray-400 hover:text-white transition cursor-pointer"
            >
              <Settings size={13} />
              <span>Settings</span>
            </button>
          </div>
        </div>
      </aside>

      {/* 2. MAIN WORKSPACE */}
      <main className="flex-1 flex flex-col h-full overflow-hidden bg-[#121316]">


        {/* Chat Feed */}
        <div className="flex-1 overflow-y-auto px-6 py-6 space-y-6 flex flex-col select-text">
          {messages.length === 0 && (
            <div className="flex-1 min-h-[460px] flex flex-col items-center justify-center text-center max-w-2xl mx-auto py-10 px-4 w-full select-none my-auto relative">
              {/* Subtle background watermark like reference */}
              <div className="absolute inset-0 pointer-events-none flex items-center justify-center opacity-[0.04]">
                <svg width="400" height="280" viewBox="0 0 400 280" fill="none" xmlns="http://www.w3.org/2000/svg">
                  <path d="M60 40L340 40L200 240Z" stroke="white" strokeWidth="8" />
                  <path d="M120 70L280 70L200 200Z" stroke="white" strokeWidth="4" />
                </svg>
              </div>

              {/* Title Header */}
              <div className="text-center mb-7 relative z-10 flex flex-col items-center">
                <h1 className="text-3xl md:text-4xl font-medium text-white tracking-tight flex items-center justify-center gap-2.5">
                  <span className="animate-gradient-flow font-bold tracking-tight">AH-Code</span>
                  <span className="text-xs font-semibold px-2.5 py-0.5 rounded-full bg-cyan-950/60 border border-cyan-500/30 text-cyan-400 tracking-wide">
                    Studio
                  </span>
                </h1>
                <p className="text-xs text-gray-400 mt-2 font-normal">
                  AI Coding Assistant พร้อมช่วยเขียนโค้ดและพัฒนาโปรเจกต์ของคุณ
                </p>
              </div>

              {/* Centered Chat Input Box */}
              <div className="w-full relative z-30">
                {renderPromptBox(true)}
              </div>

              {/* Quick Action Chips Row */}
              <div className="flex flex-wrap items-center justify-center gap-2.5 mt-5 max-w-2xl relative z-10">
                <button
                  type="button"
                  onClick={() => setInputText('ช่วยสรุปภาพรวมโครงสร้างและการทำงานของโปรเจกต์นี้ให้หน่อย')}
                  className="flex items-center gap-2 px-3.5 py-1.5 rounded-xl bg-[#16171b] hover:bg-[#202228] border border-[#2b2d35] hover:border-cyan-500/40 text-xs text-gray-300 hover:text-white transition cursor-pointer shadow-xs"
                >
                  <Clock size={13} className="text-gray-400" />
                  <span>Weekly Summary</span>
                </button>
                <button
                  type="button"
                  onClick={() => setInputText('ช่วยตรวจหาจุดผิดพลาด บั๊ก หรือจุดที่ควรปรับปรุงในโค้ดโปรเจกต์นี้')}
                  className="flex items-center gap-2 px-3.5 py-1.5 rounded-xl bg-[#16171b] hover:bg-[#202228] border border-[#2b2d35] hover:border-emerald-500/40 text-xs text-gray-300 hover:text-white transition cursor-pointer shadow-xs"
                >
                  <AlertTriangle size={13} className="text-gray-400" />
                  <span>Error Fix</span>
                </button>
                <button
                  type="button"
                  onClick={() => setInputText('ช่วยออกแบบและสร้างฟังก์ชันใหม่สำหรับโปรเจกต์นี้')}
                  className="flex items-center gap-2 px-3.5 py-1.5 rounded-xl bg-[#16171b] hover:bg-[#202228] border border-[#2b2d35] hover:border-purple-500/40 text-xs text-gray-300 hover:text-white transition cursor-pointer shadow-xs"
                >
                  <Sparkles size={13} className="text-gray-400" />
                  <span>PPT Creation</span>
                </button>
                <button
                  type="button"
                  onClick={handleRunBuild}
                  className="flex items-center gap-2 px-3.5 py-1.5 rounded-xl bg-[#16171b] hover:bg-[#202228] border border-[#2b2d35] hover:border-amber-500/40 text-xs text-gray-300 hover:text-white transition cursor-pointer shadow-xs"
                >
                  <Zap size={13} className="text-gray-400" />
                  <span>Idle-time task</span>
                </button>
              </div>
            </div>
          )}

          {messages.map((msg) => (
            <div
              key={msg.id}
              className={`w-full max-w-4xl mx-auto flex flex-col ${msg.sender === 'user' ? 'items-end' : 'items-start'}`}
            >
              {/* User Bubble */}
              {msg.sender === 'user' ? (
                <div className="max-w-[85%] bg-[#1f2128] border border-[#2d303a] rounded-xl px-3.5 py-2 text-[13.5px] text-gray-100 shadow-xs text-left select-text relative group">
                  {/* Images if attached (Antigravity compact thumbnail card) */}
                  {msg.images && msg.images.length > 0 && (
                    <div className="flex flex-wrap gap-2 mb-2">
                      {msg.images.map((img, idx) => (
                        <div
                          key={idx}
                          onClick={() => setPreviewImage(img)}
                          className="relative group/img w-20 h-20 rounded-xl overflow-hidden border border-[#383b48] hover:border-cyan-400/80 transition cursor-pointer bg-[#15161a] shadow-sm shrink-0"
                          title="คลิกเพื่อดูรูปภาพขนาดเต็ม"
                        >
                          <img
                            src={img}
                            alt={`Attachment ${idx + 1}`}
                            className="w-full h-full object-cover transition duration-200 group-hover/img:scale-110"
                          />
                          <div className="absolute inset-0 bg-black/40 opacity-0 group-hover/img:opacity-100 transition flex items-center justify-center">
                            <Eye size={16} className="text-white drop-shadow" />
                          </div>
                          <span className="absolute bottom-1 right-1 text-[9px] bg-black/75 text-gray-300 font-mono px-1 rounded">
                            IMG
                          </span>
                        </div>
                      ))}
                    </div>
                  )}
                  <p className="whitespace-pre-wrap leading-relaxed select-text cursor-text">{msg.text}</p>
                  <button
                    type="button"
                    onClick={() => copyToClipboard(msg.text, msg.id)}
                    className="opacity-0 group-hover:opacity-100 transition-opacity absolute -bottom-3 right-2 bg-[#18191d] hover:bg-[#252830] border border-[#2d303a] rounded-md px-1.5 py-0.5 text-[10px] text-gray-400 hover:text-white flex items-center gap-1 shadow-md cursor-pointer select-none"
                    title="คัดลอกข้อความ (Copy prompt)"
                  >
                    {copiedMsgId === msg.id ? (
                      <>
                        <Check size={11} className="text-emerald-400" />
                        <span className="text-emerald-400 font-sans">คัดลอกแล้ว</span>
                      </>
                    ) : (
                      <>
                        <Copy size={11} />
                        <span className="font-sans">คัดลอก</span>
                      </>
                    )}
                  </button>
                </div>
              ) : (
                /* Assistant - Clean, borderless, fast Antigravity style */
                <div className="w-full flex flex-col gap-1.5 py-0.5">
                  {/* Collapsible Action Stream (Antigravity Style) */}
                  <div className="text-xs text-gray-400 flex flex-col gap-1">
                    <button
                      onClick={() =>
                        setOpenThoughts(prev => ({ ...prev, [msg.id]: !prev[msg.id] }))
                      }
                      className="flex items-center gap-1.5 text-gray-400 hover:text-gray-200 transition cursor-pointer py-0.5 select-none text-[11px]"
                    >
                      {openThoughts[msg.id] ? <ChevronDown size={13} className="text-gray-500" /> : <ChevronRight size={13} className="text-gray-500" />}
                      <span className="font-medium text-gray-300">
                        {getActionHeader(msg.actions, msg.thought)}
                      </span>
                    </button>

                    {openThoughts[msg.id] && (
                      <div className="pl-5 flex flex-col gap-1.5 text-[11px] border-l border-[#282a32] ml-1.5 py-1">
                        {msg.thought && (() => {
                          const parts = msg.thought.split('\n\n');
                          const summary = parts[0];
                          const detailedThought = parts.slice(1).join('\n\n').trim();
                          return (
                            <div className="flex flex-col gap-2 my-0.5">
                              <div className="flex items-center gap-1 text-gray-400 font-mono">
                                <span>{summary}</span>
                                <ChevronRight size={12} className="text-gray-500" />
                              </div>
                              {detailedThought && (
                                <div className="bg-[#141519] border border-[#262831] rounded-lg p-2.5 text-gray-300 font-mono text-[11px] whitespace-pre-wrap leading-relaxed max-h-80 overflow-y-auto select-text shadow-inner">
                                  <div className="text-[10px] text-purple-400 uppercase tracking-wider font-semibold mb-1 flex items-center gap-1.5 border-b border-[#23252c] pb-1">
                                    <Brain size={12} />
                                    <span>AI Thought Process / Reasoning</span>
                                  </div>
                                  <div className="text-gray-300 text-[11px] leading-relaxed">
                                    {detailedThought}
                                  </div>
                                </div>
                              )}
                            </div>
                          );
                        })()}
                        {msg.actions && msg.actions.length > 0 ? (
                          msg.actions.map((act, aIdx) => (
                            <div key={aIdx} className="flex items-center gap-1.5 font-mono">
                              {act.type === 'status' ? (
                                <div className="flex items-center gap-1.5 text-emerald-400 text-[10px]">
                                  <Check size={11} />
                                  <span>{act.label}</span>
                                </div>
                              ) : act.type === 'view_image' ? (
                                <div className="flex items-center gap-1.5 text-purple-300">
                                  <ImageIcon size={12} className="text-purple-400 shrink-0" />
                                  <span className="text-gray-400">{act.label}</span>
                                  <span className="text-gray-200">{act.detail}</span>
                                </div>
                              ) : act.type === 'read_file' ? (
                                <div className="flex items-center gap-1.5 text-gray-300">
                                  <span className="text-gray-400">{act.label}</span>
                                  {renderStepIcon(act.icon || 'file')}
                                  <span className="text-gray-200 font-semibold">{act.detail}</span>
                                  {act.lineRange && <span className="text-gray-500 text-[10px]">{act.lineRange}</span>}
                                </div>
                              ) : act.type === 'edit_file' ? (
                                <div className="flex items-center gap-1.5 text-gray-300">
                                  <span className="text-gray-400">{act.label}</span>
                                  {renderStepIcon('edit')}
                                  <span className="text-gray-200 font-semibold">{act.detail}</span>
                                  {act.lineRange && <span className="text-gray-500 text-[10px]">{act.lineRange}</span>}
                                </div>
                              ) : (
                                <div className="flex items-center gap-1.5 text-gray-300">
                                  {renderStepIcon(act.icon || 'terminal')}
                                  <span className="text-gray-400">{act.label}</span>
                                  <span className="text-gray-200">{act.detail}</span>
                                </div>
                              )}
                            </div>
                          ))
                        ) : (
                          <div className="flex items-center gap-1.5 text-emerald-400 font-mono text-[10px]">
                            <Check size={11} />
                            <span>Finished turn</span>
                          </div>
                        )}
                      </div>
                    )}
                  </div>

                  {/* Clean Content Area (No bulky border card) */}
                  <div className="w-full flex flex-col gap-2 pt-0.5">
                    {/* Quota Exceeded Card (Only when error) */}
                    {msg.status === 'quota_exceeded' ? (
                      <div className="bg-amber-950/30 border border-amber-500/30 rounded-xl p-3 flex flex-col gap-2 text-amber-300">
                        <div className="flex items-start gap-2.5">
                          <AlertTriangle size={16} className="shrink-0 mt-0.5" />
                          <div className="flex flex-col gap-0.5">
                            <span className="font-medium text-amber-200 text-xs">Usage Limit Reached</span>
                            <span className="text-[11px] text-amber-300/80 leading-normal">{msg.text}</span>
                          </div>
                        </div>
                        <div className="flex items-center gap-2 pt-1 border-t border-amber-950/40">
                          <button
                            onClick={() => handleRefill(50)}
                            className="px-2.5 py-1 bg-amber-500 hover:bg-amber-400 text-gray-950 text-[11px] font-semibold rounded-lg transition cursor-pointer"
                          >
                            Refill 50 Tokens
                          </button>
                          <button
                            onClick={() => setIsSettingsOpen(true)}
                            className="px-2.5 py-1 bg-[#252830] hover:bg-[#2e313b] text-[11px] font-medium text-gray-300 rounded-lg transition cursor-pointer"
                          >
                            View Plan Details
                          </button>
                        </div>
                      </div>
                    ) : msg.status === 'gateway_error' ? (
                      /* Masked Gateway Error */
                      <div className="bg-rose-950/30 border border-rose-500/30 rounded-xl p-3 flex flex-col gap-1 text-rose-300">
                        <div className="flex items-center gap-2 font-medium text-xs">
                          <AlertTriangle size={14} />
                          <span>Gateway Connection Error</span>
                        </div>
                        <p className="text-[11px] text-rose-300/80 font-mono">{msg.text}</p>
                      </div>
                    ) : (
                      /* Direct Clean Text */
                      <div className="whitespace-pre-wrap space-y-2.5 font-normal text-[13.5px] leading-relaxed text-[#e4e6ea] select-text cursor-text">
                        {msg.text}
                      </div>
                    )}

                    {/* Tool Command Output Card */}
                    {msg.commandRan && (
                      <div className="mt-2 bg-[#101114] border border-[#272930] rounded-xl overflow-hidden font-mono text-xs">
                        <div className="flex items-center justify-between px-3 py-1.5 bg-[#1a1c22] border-b border-[#272930] text-gray-300">
                          <div className="flex items-center gap-2">
                            <Terminal size={13} className="text-cyan-400" />
                            <span>{msg.commandRan.command}</span>
                          </div>
                          <span className={`text-[10px] px-1.5 py-0.5 rounded ${msg.commandRan.success ? 'bg-emerald-950 text-emerald-400 border border-emerald-800/40' : 'bg-rose-950 text-rose-400 border border-rose-800/40'}`}>
                            {msg.commandRan.success ? 'Success' : `Exit ${msg.commandRan.exitCode}`}
                          </span>
                        </div>
                        <pre className="p-3 text-gray-300 overflow-x-auto whitespace-pre leading-relaxed text-[11px] select-text cursor-text">
                          {msg.commandRan.stdout || msg.commandRan.stderr || 'No output.'}
                        </pre>
                        {!msg.commandRan.success && (
                          <div className="px-3 py-2 border-t border-[#272930] bg-[#15161a]">
                            <button
                              onClick={() => handleAskAiToFixBuild(msg.commandRan!)}
                              disabled={loading}
                              className="flex items-center gap-1.5 px-2.5 py-1 bg-cyan-500/15 hover:bg-cyan-500/25 disabled:opacity-40 text-cyan-300 border border-cyan-800/40 rounded-lg text-[11px] font-medium transition cursor-pointer"
                            >
                              <Sparkles size={12} />
                              <span>Ask AI to fix this</span>
                            </button>
                          </div>
                        )}
                      </div>
                    )}

                    {/* Minimal Action Toolbar */}
                    <div className="flex items-center gap-2.5 pt-1 text-gray-500 text-xs">
                      {/* Diff Badge +N -M > */}
                      {msg.diffStats && (msg.diffStats.additions > 0 || msg.diffStats.deletions > 0) && (
                        <button
                          type="button"
                          onClick={() => setViewingDiff(msg.diffStats!)}
                          className="flex items-center gap-1.5 px-2 py-0.5 rounded-md bg-[#191b22] hover:bg-[#222530] border border-[#2b2e3a] hover:border-emerald-500/40 text-[11px] font-mono transition cursor-pointer group shadow-xs mr-1"
                          title="คลิกเพื่อดูรายละเอียดโค้ดที่แก้ไข (Diff Inspector)"
                        >
                          <span className="text-emerald-400 font-semibold">+{msg.diffStats.additions}</span>
                          <span className="text-rose-400 font-semibold">-{msg.diffStats.deletions}</span>
                          {msg.diffStats.files && msg.diffStats.files.length > 0 && (
                            <span className="text-gray-400 text-[10px]">
                              ({msg.diffStats.files.length} file{msg.diffStats.files.length > 1 ? 's' : ''})
                            </span>
                          )}
                          <ChevronRight size={12} className="text-gray-500 group-hover:text-emerald-400 group-hover:translate-x-0.5 transition-transform" />
                        </button>
                      )}
                      <button
                        type="button"
                        onClick={() => copyToClipboard(msg.text, msg.id)}
                        className="hover:text-gray-300 transition cursor-pointer p-0.5 flex items-center gap-1"
                        title="คัดลอกข้อความ (Copy response)"
                      >
                        {copiedMsgId === msg.id ? (
                          <>
                            <Check size={12} className="text-emerald-400" />
                            <span className="text-[10px] text-emerald-400 font-sans">คัดลอกแล้ว</span>
                          </>
                        ) : (
                          <Copy size={12} />
                        )}
                      </button>
                      <button className="hover:text-gray-300 transition cursor-pointer p-0.5" title="Good response">
                        <ThumbsUp size={12} />
                      </button>
                      <button className="hover:text-gray-300 transition cursor-pointer p-0.5" title="Bad response">
                        <ThumbsDown size={12} />
                      </button>
                      <span className="ml-auto text-[10px] text-gray-600 font-mono">{msg.timestamp}</span>
                    </div>
                  </div>
                </div>
              )}
            </div>
          ))}

          {loading && (
            <div className="w-full max-w-4xl mx-auto flex flex-col gap-1.5 py-1 text-xs">
              <div className="flex items-center gap-1.5 text-gray-300 font-medium">
                <ChevronDown size={14} className="text-gray-400" />
                <span>
                  {(() => {
                    const editStep = liveSteps.find(s => s.icon === 'edit' || s.title.toLowerCase().includes('editing'));
                    const readSteps = liveSteps.filter(s => s.icon === 'file' || s.icon === 'code' || s.title.toLowerCase().includes('analyzed') || s.title.toLowerCase().includes('reading'));
                    if (editStep) {
                      return editStep.title;
                    }
                    if (readSteps.length > 0) {
                      return `Exploring ${readSteps.length} file${readSteps.length > 1 ? 's' : ''}`;
                    }
                    if (liveSteps.length > 0) {
                      return liveSteps.filter(s => s.status === 'running').pop()?.title || liveSteps[liveSteps.length - 1].title;
                    }
                    if (activeLoadingContext.type === 'file') {
                      return `Exploring 1 file`;
                    }
                    if (activeLoadingContext.type === 'image') {
                      return `Analyzing image context`;
                    }
                    return `Thinking...`;
                  })()}
                </span>
              </div>
              <div className="pl-5 flex flex-col gap-1.5 text-[11px] border-l border-[#282a32] ml-1.5">
                {/* Collapsible Live Thought Process (Collapsed by default with > chevron) */}
                <button
                  type="button"
                  onClick={() => setIsLiveThoughtOpen(prev => !prev)}
                  className="flex items-center gap-1.5 text-gray-400 hover:text-gray-200 transition cursor-pointer select-none font-mono text-[11px] w-fit py-0.5"
                  title="คลิกเพื่อดูกระบวนการคิดสด (Live Thought)"
                >
                  {isLiveThoughtOpen ? (
                    <ChevronDown size={13} className="text-gray-400" />
                  ) : (
                    <ChevronRight size={13} className="text-gray-400" />
                  )}
                  <span>Thought for {activeThoughtElapsed}s</span>
                  {liveThoughtText && !isLiveThoughtOpen && (
                    <span className="text-[10px] text-purple-400/80 font-sans ml-1">
                      (คลิกเพื่อดูวิธีคิด)
                    </span>
                  )}
                </button>

                {isLiveThoughtOpen && liveThoughtText && (
                  <div className="bg-[#121316] border border-[#23252d] rounded-lg p-2.5 text-gray-300 font-mono text-[11px] leading-relaxed max-h-52 overflow-y-auto select-text shadow-lg my-1 animate-in fade-in duration-150">
                    <div className="text-[10px] text-purple-400 uppercase tracking-wider font-semibold mb-1 flex items-center gap-1.5 border-b border-[#1f2128] pb-1">
                      <Brain size={12} className="text-purple-400 animate-pulse" />
                      <span>Live Reasoning</span>
                      <span className="w-1.5 h-1.5 rounded-full bg-purple-400 animate-ping ml-auto"></span>
                    </div>
                    <div className="whitespace-pre-wrap text-gray-300 font-mono text-[11px] leading-relaxed">
                      {liveThoughtText}
                    </div>
                  </div>
                )}

                {liveSteps.length > 0 ? (
                  <div className="flex flex-col gap-1.5 py-0.5">
                    {liveSteps.map((step) => {
                      const isFileAnalysis = step.title.startsWith('Analyzed ') || step.title.startsWith('Reading ');
                      if (isFileAnalysis) {
                        const parts = step.title.split(' ');
                        const verb = parts[0];
                        const fileName = parts.slice(1).join(' ') || step.detail;
                        return (
                          <div key={step.id} className="flex items-center gap-1.5 font-mono text-[11px]">
                            <span className="text-gray-400">{verb}</span>
                            {renderStepIcon(step.icon || 'file')}
                            <span className="text-gray-200 font-semibold">{fileName}</span>
                            {step.detail && step.detail !== fileName && (
                              <span className="text-gray-500 text-[10px]">{step.detail}</span>
                            )}
                            {step.status === 'running' ? (
                              <span className="w-1.5 h-1.5 rounded-full bg-cyan-400 animate-pulse ml-auto shrink-0" />
                            ) : step.status === 'done' ? (
                              <Check size={12} className="text-emerald-400 ml-auto shrink-0" />
                            ) : null}
                          </div>
                        );
                      }
                      return (
                        <div key={step.id} className="flex items-center gap-2 font-mono text-[11px]">
                          {renderStepIcon(step.icon)}
                          <span className={step.status === 'running' ? 'text-cyan-300 font-medium' : 'text-gray-300'}>
                            {step.title}
                          </span>
                          {step.detail && (
                            <span className="text-[10px] bg-[#22242b] text-gray-400 px-1.5 py-0.5 rounded border border-[#2e313b] max-w-xs truncate">
                              {step.detail}
                            </span>
                          )}
                          {step.status === 'running' ? (
                            <span className="w-1.5 h-1.5 rounded-full bg-cyan-400 animate-pulse ml-auto shrink-0" />
                          ) : step.status === 'done' ? (
                            <Check size={12} className="text-emerald-400 ml-auto shrink-0" />
                          ) : null}
                        </div>
                      );
                    })}
                  </div>
                ) : (
                  <>
                    {activeLoadingContext.type === 'image' ? (
                      <div className="flex items-center gap-1.5 text-purple-300 font-mono">
                        <ImageIcon size={12} className="text-purple-400 shrink-0" />
                        <span className="text-gray-400">Inspecting</span>
                        <span className="text-gray-200">{activeLoadingContext.text}</span>
                      </div>
                    ) : activeLoadingContext.type === 'file' ? (
                      <div className="flex items-center gap-1.5 text-gray-300 font-mono">
                        <span className="text-gray-400">Analyzing</span>
                        <FileText size={12} className="text-cyan-400 shrink-0" />
                        <span className="text-gray-100 font-semibold">{activeLoadingContext.file}</span>
                      </div>
                    ) : (
                      <div className="flex items-center gap-1.5 text-gray-400 font-mono">
                        <span>Synthesizing response with {selectedModel}...</span>
                      </div>
                    )}
                  </>
                )}

                <div className="flex items-center gap-2 text-cyan-400 font-medium pt-0.5">
                  <span className="w-1.5 h-1.5 rounded-full bg-cyan-400 animate-pulse"></span>
                  <span>{liveSteps.filter(s => s.status === 'running').pop()?.title || 'Working..'}</span>
                </div>
              </div>
            </div>
          )}

          <div ref={chatEndRef} />
        </div>

        {/* 3. BOTTOM FLOATING PROMPT BOX */}
        {messages.length > 0 && (
          <div className="p-5 max-w-4xl w-full mx-auto shrink-0">
            {renderPromptBox(false)}
          </div>
        )}
      </main>
      </div>

      {/* HARDWARE COMPATIBILITY CHECK MODAL */}
      {compatibilityModal && (
        <div className="fixed inset-0 bg-black/75 backdrop-blur-xs flex items-center justify-center z-50 p-4 animate-in fade-in duration-150">
          <div className="bg-[#141518] border border-[#272931] rounded-2xl w-full max-w-lg shadow-2xl overflow-hidden flex flex-col text-xs text-gray-300">
            {/* Header */}
            <div className="flex items-center justify-between px-5 py-4 border-b border-[#22242c] bg-[#101114]">
              <div className="flex items-center gap-2.5">
                <div className="w-8 h-8 rounded-lg bg-emerald-500/15 border border-emerald-500/30 flex items-center justify-center text-emerald-400">
                  <Cpu size={17} />
                </div>
                <div className="flex flex-col">
                  <h3 className="text-sm font-semibold text-white">ตรวจสอบความเหมาะสมของระบบ (Hardware Check)</h3>
                  <span className="text-[11px] text-gray-400 font-mono">
                    โมเดล: {compatibilityModal.modelName} ({compatibilityModal.tierLabel}) • ขนาดดาวน์โหลด ~{compatibilityModal.downloadSizeGb} GB
                  </span>
                </div>
              </div>
              <button
                onClick={() => setCompatibilityModal(null)}
                className="text-gray-400 hover:text-white transition cursor-pointer p-1 rounded-lg hover:bg-[#20222a]"
              >
                <X size={16} />
              </button>
            </div>

            {/* Body */}
            <div className="p-5 flex flex-col gap-4 text-xs">
              {/* Specs Grid */}
              <div className="grid grid-cols-2 gap-2.5">
                <div className="bg-[#0f1013] border border-[#20222a] rounded-xl p-3 flex flex-col gap-1">
                  <div className="flex items-center gap-1.5 text-gray-400 text-[11px]">
                    <Brain size={13} className="text-purple-400" />
                    <span>System RAM</span>
                  </div>
                  <div className="flex items-baseline gap-1.5 mt-0.5">
                    <span className="text-sm font-bold text-white">{compatibilityModal.specs.totalRamGb} GB</span>
                    <span className="text-[10px] text-gray-500">(ว่าง {compatibilityModal.specs.freeRamGb} GB)</span>
                  </div>
                  <span className="text-[10px] text-gray-400">ต้องการขั้นต่ำ: {compatibilityModal.requiredRamGb} GB</span>
                </div>

                <div className="bg-[#0f1013] border border-[#20222a] rounded-xl p-3 flex flex-col gap-1">
                  <div className="flex items-center gap-1.5 text-gray-400 text-[11px]">
                    <Cpu size={13} className="text-cyan-400" />
                    <span>GPU VRAM</span>
                  </div>
                  <div className="flex items-baseline gap-1.5 mt-0.5">
                    <span className="text-sm font-bold text-white">{compatibilityModal.specs.vramGb > 0 ? `${compatibilityModal.specs.vramGb} GB` : 'None'}</span>
                    <span className="text-[10px] text-gray-500">{compatibilityModal.specs.hasNvidiaGpu ? '(NVIDIA CUDA)' : '(iGPU)'}</span>
                  </div>
                  <span className="text-[10px] text-gray-400 truncate" title={compatibilityModal.specs.gpuName}>
                    {compatibilityModal.specs.gpuName}
                  </span>
                </div>

                <div className="bg-[#0f1013] border border-[#20222a] rounded-xl p-3 flex flex-col gap-1">
                  <div className="flex items-center gap-1.5 text-gray-400 text-[11px]">
                    <HardDrive size={13} className="text-amber-400" />
                    <span>พื้นที่ฮาร์ดดิสก์</span>
                  </div>
                  <div className="flex items-baseline gap-1.5 mt-0.5">
                    <span className="text-sm font-bold text-white">{compatibilityModal.specs.freeDiskGb} GB</span>
                    <span className="text-[10px] text-emerald-400">ว่าง</span>
                  </div>
                  <span className="text-[10px] text-gray-400">ขนาดไฟล์: ~{compatibilityModal.downloadSizeGb} GB</span>
                </div>

                <div className="bg-[#0f1013] border border-[#20222a] rounded-xl p-3 flex flex-col gap-1">
                  <div className="flex items-center gap-1.5 text-gray-400 text-[11px]">
                    <Zap size={13} className="text-emerald-400" />
                    <span>ความเร็วที่คาดการณ์</span>
                  </div>
                  <div className="mt-0.5">
                    <span className="text-xs font-bold text-emerald-400 font-mono">{compatibilityModal.estimatedSpeed}</span>
                  </div>
                  <span className="text-[10px] text-gray-400">ประเมินจากสเปกเครื่องของคุณ</span>
                </div>
              </div>

              {/* Verdict Banner */}
              <div className={`rounded-xl p-3.5 border flex items-start gap-3 ${
                compatibilityModal.verdict === 'optimal'
                  ? 'bg-emerald-950/40 border-emerald-500/40 text-emerald-300'
                  : compatibilityModal.verdict === 'good'
                  ? 'bg-emerald-950/30 border-emerald-600/35 text-emerald-300'
                  : compatibilityModal.verdict === 'moderate'
                  ? 'bg-amber-950/40 border-amber-500/40 text-amber-300'
                  : 'bg-rose-950/40 border-rose-500/40 text-rose-300'
              }`}>
                {compatibilityModal.verdict === 'optimal' || compatibilityModal.verdict === 'good' ? (
                  <CheckCircle2 size={18} className="text-emerald-400 shrink-0 mt-0.5" />
                ) : compatibilityModal.verdict === 'moderate' ? (
                  <AlertTriangle size={18} className="text-amber-400 shrink-0 mt-0.5" />
                ) : (
                  <AlertTriangle size={18} className="text-rose-400 shrink-0 mt-0.5" />
                )}
                <div className="flex flex-col gap-1">
                  <span className="font-semibold text-xs text-white">{compatibilityModal.verdictTitle}</span>
                  <p className="text-[11px] leading-relaxed opacity-90">{compatibilityModal.verdictDetail}</p>
                </div>
              </div>
            </div>

            {/* Footer */}
            <div className="flex items-center justify-end gap-2.5 px-5 py-3.5 border-t border-[#22242c] bg-[#101114]">
              <button
                onClick={() => setCompatibilityModal(null)}
                className="px-3.5 py-1.5 rounded-lg text-xs font-medium text-gray-300 hover:text-white hover:bg-[#20222a] transition cursor-pointer"
              >
                ยกเลิก
              </button>
              <button
                disabled={!compatibilityModal.canInstall}
                onClick={() => {
                  const id = compatibilityModal.modelId;
                  setCompatibilityModal(null);
                  setIsModelDropdownOpen(false);
                  setIsQwenSubmenuOpen(false);
                  setIsClaudeSubmenuOpen(false);
                  handleInstallModel(id);
                }}
                className={`flex items-center gap-1.5 px-4 py-1.5 rounded-lg text-xs font-semibold transition cursor-pointer ${
                  compatibilityModal.verdict === 'warning'
                    ? 'bg-amber-600 hover:bg-amber-500 text-gray-950'
                    : 'bg-emerald-500 hover:bg-emerald-400 text-gray-950 shadow-md shadow-emerald-500/20'
                }`}
              >
                <Download size={13} />
                <span>ยืนยันการติดตั้ง ({compatibilityModal.downloadSizeGb} GB)</span>
              </button>
            </div>
          </div>
        </div>
      )}

      {/* DIFF VIEWER MODAL (Code Changes Inspector) */}
      {viewingDiff && (
        <div className="fixed inset-0 bg-black/75 backdrop-blur-xs flex items-center justify-center z-50 p-4">
          <div className="bg-[#141518] border border-[#272931] rounded-2xl w-full max-w-4xl max-h-[85vh] shadow-2xl flex flex-col overflow-hidden animate-in fade-in zoom-in-95 duration-150">
            {/* Header */}
            <div className="flex items-center justify-between px-5 py-3.5 border-b border-[#22242c] bg-[#101114]">
              <div className="flex items-center gap-3">
                <div className="p-1.5 rounded-lg bg-emerald-950/50 border border-emerald-800/40 text-emerald-400">
                  <FileCode size={16} />
                </div>
                <div className="flex flex-col">
                  <div className="flex items-center gap-2">
                    <span className="text-sm font-semibold text-white">Workspace Changes</span>
                    <div className="flex items-center gap-1.5 font-mono text-xs px-2 py-0.5 rounded bg-[#1c1e26] border border-[#2d303d]">
                      <span className="text-emerald-400 font-bold">+{viewingDiff.additions}</span>
                      <span className="text-rose-400 font-bold">-{viewingDiff.deletions}</span>
                    </div>
                  </div>
                  {viewingDiff.files && viewingDiff.files.length > 0 && (
                    <span className="text-[11px] text-gray-400 truncate max-w-lg">
                      {viewingDiff.files.length} file{viewingDiff.files.length > 1 ? 's' : ''} modified: {viewingDiff.files.join(', ')}
                    </span>
                  )}
                </div>
              </div>
              <button
                onClick={() => setViewingDiff(null)}
                className="p-1.5 rounded-lg text-gray-400 hover:text-white hover:bg-[#20222a] transition cursor-pointer"
              >
                <X size={16} />
              </button>
            </div>

            {/* Diff Content */}
            <div className="flex-1 overflow-auto p-4 bg-[#0d0e11] font-mono text-xs custom-scrollbar">
              {viewingDiff.diffText ? (
                <div className="space-y-0.5">
                  {viewingDiff.diffText.split('\n').map((line, idx) => {
                    const isAdd = line.startsWith('+') && !line.startsWith('+++');
                    const isDel = line.startsWith('-') && !line.startsWith('---');
                    const isHunk = line.startsWith('@@');
                    const isFileHeader = line.startsWith('diff --git') || line.startsWith('index ') || line.startsWith('---') || line.startsWith('+++');

                    return (
                      <div
                        key={idx}
                        className={`px-2 py-0.5 rounded-xs leading-relaxed whitespace-pre font-mono ${
                          isAdd
                            ? 'bg-emerald-950/40 text-emerald-300 border-l-2 border-emerald-500'
                            : isDel
                            ? 'bg-rose-950/40 text-rose-300 border-l-2 border-rose-500'
                            : isHunk
                            ? 'bg-cyan-950/30 text-cyan-400 font-semibold my-1'
                            : isFileHeader
                            ? 'text-gray-500 font-bold mt-2 pb-0.5 border-b border-[#1f2129]'
                            : 'text-gray-300'
                        }`}
                      >
                        {line || ' '}
                      </div>
                    );
                  })}
                </div>
              ) : (
                <div className="text-center py-12 text-gray-500">
                  No unified diff text recorded.
                </div>
              )}
            </div>

            {/* Footer */}
            <div className="flex items-center justify-between px-5 py-3 border-t border-[#22242c] bg-[#101114] text-xs">
              <span className="text-gray-500 text-[11px]">Diff inspection powered by git tracking</span>
              <button
                onClick={() => setViewingDiff(null)}
                className="px-3 py-1.5 rounded-lg bg-[#20222a] hover:bg-[#2a2d38] text-gray-200 transition cursor-pointer"
              >
                Close
              </button>
            </div>
          </div>
        </div>
      )}

      {/* 4. ANTIGRAVITY PRO SETTINGS MODAL */}
      {isSettingsOpen && (
        <div className="fixed inset-0 bg-black/75 backdrop-blur-xs flex items-center justify-center z-50 p-4">
          <div className="bg-[#141518] border border-[#272931] rounded-2xl w-full max-w-4xl h-[610px] shadow-2xl overflow-hidden flex flex-row text-xs text-gray-300 animate-in fade-in zoom-in-95 duration-150">
            {/* Left Sidebar */}
            <div className="w-60 bg-[#0f1013] border-r border-[#22242c] flex flex-col justify-between p-3 select-none shrink-0">
              <div className="flex flex-col gap-4 overflow-y-auto custom-scrollbar pr-1">
                {/* SETTINGS GROUP */}
                <div className="flex flex-col gap-0.5">
                  <span className="text-[10px] font-bold text-gray-500 uppercase tracking-wider px-2.5 py-1">Settings</span>
                  
                  <button
                    onClick={() => setSettingsTab('general')}
                    className={`flex items-center gap-2.5 px-3 py-2 rounded-lg text-left font-medium transition cursor-pointer ${settingsTab === 'general' ? 'bg-[#22252e] text-white shadow-xs' : 'text-gray-400 hover:text-gray-200 hover:bg-[#1a1b22]'}`}
                  >
                    <Sliders size={15} className={settingsTab === 'general' ? 'text-cyan-400' : 'text-gray-400'} />
                    <span>General</span>
                  </button>

                  <button
                    onClick={() => setSettingsTab('application')}
                    className={`flex items-center gap-2.5 px-3 py-2 rounded-lg text-left font-medium transition cursor-pointer ${settingsTab === 'application' ? 'bg-[#22252e] text-white shadow-xs' : 'text-gray-400 hover:text-gray-200 hover:bg-[#1a1b22]'}`}
                  >
                    <Terminal size={15} className={settingsTab === 'application' ? 'text-cyan-400' : 'text-gray-400'} />
                    <span>Application</span>
                  </button>

                  <button
                    onClick={() => setSettingsTab('appearance')}
                    className={`flex items-center gap-2.5 px-3 py-2 rounded-lg text-left font-medium transition cursor-pointer ${settingsTab === 'appearance' ? 'bg-[#22252e] text-white shadow-xs' : 'text-gray-400 hover:text-gray-200 hover:bg-[#1a1b22]'}`}
                  >
                    <Palette size={15} className={settingsTab === 'appearance' ? 'text-cyan-400' : 'text-gray-400'} />
                    <span>Appearance</span>
                  </button>

                  <button
                    onClick={() => setSettingsTab('models')}
                    className={`flex items-center justify-between px-3 py-2 rounded-lg text-left font-medium transition cursor-pointer ${settingsTab === 'models' ? 'bg-[#22252e] text-white shadow-xs' : 'text-gray-400 hover:text-gray-200 hover:bg-[#1a1b22]'}`}
                  >
                    <div className="flex items-center gap-2.5">
                      <Cpu size={15} className={settingsTab === 'models' ? 'text-cyan-400' : 'text-gray-400'} />
                      <span>Models</span>
                    </div>
                    {grokTier !== 'NONE' && (
                      <span className="text-[9px] px-1.5 py-0.2 rounded bg-cyan-950 text-cyan-400 border border-cyan-800/40 font-mono">
                        {grokTier}
                      </span>
                    )}
                  </button>

                  <button
                    onClick={() => setSettingsTab('customizations')}
                    className={`flex items-center gap-2.5 px-3 py-2 rounded-lg text-left font-medium transition cursor-pointer ${settingsTab === 'customizations' ? 'bg-[#22252e] text-white shadow-xs' : 'text-gray-400 hover:text-gray-200 hover:bg-[#1a1b22]'}`}
                  >
                    <FileCode size={15} className={settingsTab === 'customizations' ? 'text-cyan-400' : 'text-gray-400'} />
                    <span>Customizations</span>
                  </button>

                  <button
                    onClick={() => setSettingsTab('browser')}
                    className={`flex items-center gap-2.5 px-3 py-2 rounded-lg text-left font-medium transition cursor-pointer ${settingsTab === 'browser' ? 'bg-[#22252e] text-white shadow-xs' : 'text-gray-400 hover:text-gray-200 hover:bg-[#1a1b22]'}`}
                  >
                    <Globe size={15} className={settingsTab === 'browser' ? 'text-cyan-400' : 'text-gray-400'} />
                    <span>Browser</span>
                  </button>
                </div>

                {/* PROJECTS GROUP */}
                <div className="flex flex-col gap-0.5 pt-1">
                  <span className="text-[10px] font-bold text-gray-500 uppercase tracking-wider px-2.5 py-1">Projects</span>
                  <div className="flex items-center gap-2 px-3 py-1.5 rounded-lg text-gray-200 bg-[#1c1e26] border border-[#2b2d38] font-medium text-[11px]">
                    <Folder size={13} className="text-cyan-400" />
                    <span className="truncate">Sent-Code</span>
                  </div>
                  <div className="flex items-center gap-2 px-3 py-1.5 rounded-lg text-gray-400 hover:text-gray-200 hover:bg-[#1a1b22] transition cursor-pointer text-[11px]">
                    <Folder size={13} className="text-gray-500" />
                    <span>payment</span>
                  </div>
                  <div className="flex items-center gap-2 px-3 py-1.5 rounded-lg text-gray-400 hover:text-gray-200 hover:bg-[#1a1b22] transition cursor-pointer text-[11px]">
                    <Folder size={13} className="text-gray-500" />
                    <span>football</span>
                  </div>
                  <button
                    onClick={handleSelectFolder}
                    className="text-left px-3 py-1 text-[11px] text-cyan-400 hover:underline pt-0.5 cursor-pointer"
                  >
                    Show All
                  </button>
                </div>

                {/* NOT IN PROJECT */}
                <div className="flex flex-col gap-0.5 pt-1">
                  <span className="text-[10px] font-bold text-gray-500 uppercase tracking-wider px-2.5 py-1">Not in Project</span>
                  <div className="flex items-center gap-2 px-3 py-1.5 rounded-lg text-gray-400 hover:text-gray-200 hover:bg-[#1a1b22] transition cursor-pointer text-[11px]">
                    <MessageSquare size={13} className="text-gray-500" />
                    <span>Conversations</span>
                  </div>
                </div>
              </div>

              {/* Bottom Items */}
              <div className="pt-2 border-t border-[#1e2028] flex flex-col gap-2">
                <div className="flex flex-col gap-0.5">
                  <button className="flex items-center gap-2 px-2.5 py-1 text-gray-400 hover:text-gray-200 text-[11px] transition text-left cursor-pointer">
                    <HelpCircle size={13} />
                    <span>Shortcuts</span>
                  </button>
                  <button className="flex items-center gap-2 px-2.5 py-1 text-gray-400 hover:text-gray-200 text-[11px] transition text-left cursor-pointer">
                    <MessageSquare size={13} />
                    <span>Provide Feedback</span>
                  </button>
                </div>

                {/* User Profile Card */}
                <div className="flex items-center gap-2.5 bg-[#17181f] p-2 rounded-xl border border-[#272933]">
                  <div className="w-7 h-7 rounded-full bg-gradient-to-tr from-rose-500 to-amber-500 text-white font-bold flex items-center justify-center text-xs shrink-0 shadow">
                    X
                  </div>
                  <div className="flex flex-col overflow-hidden">
                    <span className="font-semibold text-white text-[11px] leading-tight">X SENSER</span>
                    <span className="text-[10px] text-gray-400 truncate font-mono">{userEmail || 'hack4rwin@gmail.com'}</span>
                  </div>
                </div>
              </div>
            </div>

            {/* Right Content Pane */}
            <div className="flex-1 flex flex-col bg-[#141518] overflow-hidden">
              {/* Header */}
              <div className="flex items-center justify-between px-7 py-4 border-b border-[#21232b]">
                <div className="flex flex-col">
                  <h2 className="text-base font-semibold text-white">
                    {settingsTab === 'general' && 'General'}
                    {settingsTab === 'application' && 'Application'}
                    {settingsTab === 'appearance' && 'Appearance'}
                    {settingsTab === 'models' && 'Models & Inference'}
                    {settingsTab === 'customizations' && 'Customizations'}
                    {settingsTab === 'browser' && 'Browser'}
                  </h2>
                  <p className="text-[11px] text-gray-400">
                    {settingsTab === 'general' && 'Configure agent execution, queued message delivery, and permissions.'}
                    {settingsTab === 'application' && 'Desktop environment, workspace paths, and system integrations.'}
                    {settingsTab === 'appearance' && 'Customize editor theme, typography, and layout preferences.'}
                    {settingsTab === 'models' && 'Configure official Grok Build cloud connection and local GPU sidecars.'}
                    {settingsTab === 'customizations' && 'Manage skills, coding rules, and external MCP tool gateways.'}
                    {settingsTab === 'browser' && 'Configure web search and headless browsing capabilities.'}
                  </p>
                </div>
                <button
                  onClick={() => setIsSettingsOpen(false)}
                  className="text-gray-400 hover:text-white p-1.5 rounded-lg hover:bg-[#20222a] transition cursor-pointer"
                >
                  <X size={16} />
                </button>
              </div>

              {/* Body */}
              <div className="flex-1 overflow-y-auto p-7 flex flex-col gap-6 custom-scrollbar text-xs">
                {/* 1. GENERAL TAB */}
                {settingsTab === 'general' && (
                  <div className="flex flex-col gap-6">
                    {/* Execution */}
                    <div className="flex flex-col gap-3">
                      <span className="font-semibold text-gray-200">Execution</span>
                      <div className="flex items-center justify-between py-1">
                        <div className="flex flex-col">
                          <span className="font-medium text-white">Queued Messages</span>
                          <span className="text-[11px] text-gray-400">Configure when follow-up messages are sent.</span>
                        </div>
                        <div className="flex items-center bg-[#101115] border border-[#262832] rounded-lg p-0.5">
                          <button
                            onClick={() => setQueuedMessages('queue')}
                            className={`px-3 py-1 rounded-md text-[11px] font-medium transition cursor-pointer ${queuedMessages === 'queue' ? 'bg-[#262934] text-white shadow-xs' : 'text-gray-400 hover:text-gray-200'}`}
                          >
                            Queue
                          </button>
                          <button
                            onClick={() => setQueuedMessages('send_immediately')}
                            className={`px-3 py-1 rounded-md text-[11px] font-medium transition cursor-pointer ${queuedMessages === 'send_immediately' ? 'bg-[#262934] text-white shadow-xs' : 'text-gray-400 hover:text-gray-200'}`}
                          >
                            Send Immediately
                          </button>
                        </div>
                      </div>
                    </div>

                    <div className="border-t border-[#1e2027]" />

                    {/* Global Permissions */}
                    <div className="flex flex-col gap-4">
                      <span className="font-semibold text-gray-200">Global Permissions</span>
                      <div className="flex items-center justify-between">
                        <div className="flex flex-col">
                          <span className="font-medium text-white">Security Preset</span>
                          <span className="text-[11px] text-gray-400">Controls the actions the agent can take.</span>
                        </div>
                        <select
                          value={securityPreset}
                          onChange={(e) => setSecurityPreset(e.target.value)}
                          className="bg-[#1b1c24] border border-[#2b2d38] text-white rounded-lg px-3 py-1.5 text-xs focus:outline-none focus:border-cyan-500 cursor-pointer"
                        >
                          <option value="Default">Default</option>
                          <option value="Strict">Strict (Prompt Every Action)</option>
                          <option value="Bypass">Bypass Permissions (Auto)</option>
                        </select>
                      </div>

                      <div className="flex items-center justify-between">
                        <div className="flex flex-col">
                          <span className="font-medium text-white">Tool Permissions</span>
                          <span className="text-[11px] text-gray-400">Modify permissions for file, terminal, and MCP tools.</span>
                        </div>
                        <button className="px-3.5 py-1.5 bg-[#20222a] hover:bg-[#282b35] text-white rounded-lg transition cursor-pointer font-medium border border-[#2b2d38]">
                          Open
                        </button>
                      </div>
                    </div>

                    <div className="border-t border-[#1e2027]" />

                    {/* Agent Behavior */}
                    <div className="flex flex-col gap-3">
                      <span className="font-semibold text-gray-200">Agent Behavior</span>
                      <div className="flex items-center justify-between">
                        <div className="flex flex-col">
                          <span className="font-medium text-white">Plan Review Policy</span>
                          <span className="text-[11px] text-gray-400">Whether the agent asks you to review its documents. Type <span className="text-cyan-400 font-mono">/plan</span> to create a plan.</span>
                        </div>
                        <select
                          value={planReviewPolicy}
                          onChange={(e) => setPlanReviewPolicy(e.target.value)}
                          className="bg-[#1b1c24] border border-[#2b2d38] text-white rounded-lg px-3 py-1.5 text-xs focus:outline-none focus:border-cyan-500 cursor-pointer"
                        >
                          <option value="Always Ask">Always Ask</option>
                          <option value="Auto Approve">Auto Approve</option>
                          <option value="Never Ask">Never Ask</option>
                        </select>
                      </div>
                    </div>

                    <div className="border-t border-[#1e2027]" />

                    {/* Network Permissions */}
                    <div className="flex flex-col gap-3">
                      <span className="font-semibold text-gray-200">Network Permissions</span>
                      <div className="flex items-center justify-between">
                        <div className="flex flex-col">
                          <span className="font-medium text-white">Network Access Rules</span>
                          <span className="text-[11px] text-gray-400">Configure allowed and denied URLs for reading.</span>
                        </div>
                        <button className="px-3.5 py-1.5 bg-[#20222a] hover:bg-[#282b35] text-white rounded-lg transition cursor-pointer font-medium border border-[#2b2d38]">
                          Open
                        </button>
                      </div>
                    </div>
                  </div>
                )}

                {/* 2. MODELS TAB */}
                {settingsTab === 'models' && (
                  <div className="flex flex-col gap-5">
                    {/* SuperGrok Live Card */}
                    <div className="flex flex-col gap-3 bg-[#191a20] p-4 rounded-xl border border-[#292c36]">
                      <div className="flex items-center justify-between">
                        <div className="flex items-center gap-2 font-medium text-white">
                          <Sparkles size={16} className="text-cyan-400" />
                          <span>Official Grok Build Engine & Subscription</span>
                        </div>
                        <span className={`px-2.5 py-0.5 rounded text-[10px] font-bold tracking-wider uppercase ${grokTier === 'NONE' ? 'bg-gray-800 text-gray-400' : 'bg-cyan-950 text-cyan-300 border border-cyan-700/50 shadow-xs'}`}>
                          {grokTier === 'NONE' ? 'Not Connected' : grokTier}
                        </span>
                      </div>

                      {grokTier === 'NONE' ? (
                        <div className="flex flex-col gap-2.5">
                          <p className="text-[11px] text-gray-400 leading-relaxed">
                            Connect your official SuperGrok account to unlock Grok modes (Fast, Auto, Expert, Heavy) directly through your local engine with zero API fees.
                          </p>
                          <button
                            onClick={handleTriggerLogin}
                            className="flex items-center justify-center gap-1.5 px-3 py-2 bg-cyan-500 hover:bg-cyan-400 text-gray-950 rounded-lg transition cursor-pointer font-semibold text-xs shadow-md shadow-cyan-500/10"
                          >
                            <ExternalLink size={13} />
                            <span>Log in with Official SuperGrok (OAuth)</span>
                          </button>
                        </div>
                      ) : (
                        <div className="flex flex-col gap-3">
                          <div className="flex items-center justify-between bg-[#131418] px-3.5 py-2.5 rounded-lg border border-[#242630]">
                            <div className="flex flex-col">
                              <span className="text-[10px] text-gray-400">Authenticated Account</span>
                              <span className="text-xs text-white font-mono font-medium">{userEmail || 'hack4rwin@gmail.com'}</span>
                            </div>
                            <div className="flex items-center gap-1.5 text-[11px] text-emerald-400 bg-emerald-950/60 px-2.5 py-1 rounded-lg border border-emerald-800/40">
                              <span className="w-1.5 h-1.5 rounded-full bg-emerald-400 animate-pulse"></span>
                              <span>Synced live with grok.com</span>
                            </div>
                          </div>

                          {/* Antigravity-Style Grok Rate Limits Remaining (Matching Image 2) */}
                          <div className="flex flex-col gap-2 pt-1">
                            <div className="flex items-center gap-1.5 text-gray-300 font-medium text-xs">
                              <span>Grok Models</span>
                              <div
                                className="w-3.5 h-3.5 rounded-full border border-gray-600 text-gray-400 flex items-center justify-center text-[10px] cursor-help font-serif"
                                title="Rate limits remaining for Grok models under your current plan"
                              >
                                i
                              </div>
                            </div>

                            <div className="bg-[#121317] border border-[#252834] rounded-xl overflow-hidden divide-y divide-[#1e202a]">
                              {/* Weekly Limit Remaining */}
                              <div className="p-3.5 flex items-center justify-between">
                                <div className="flex flex-col gap-0.5">
                                  <span className="font-medium text-white text-xs">Weekly Limit Remaining</span>
                                  <span className="text-[11px] text-gray-400">
                                    You have used some of your weekly limit, it will fully refresh in {grokQuota?.weeklyRefreshTime || '1 day, 12 hours'}.
                                  </span>
                                </div>
                                <div className="flex items-center gap-2.5 shrink-0 ml-4">
                                  <span className="font-semibold text-xs text-white tracking-tight">{grokQuota?.weeklyRemainingPct ?? 78}%</span>
                                  {/* Circular Progress Ring */}
                                  <div className="relative w-6 h-6 flex items-center justify-center">
                                    <svg className="w-full h-full -rotate-90" viewBox="0 0 36 36">
                                      <path
                                        className="text-[#252834]"
                                        strokeWidth="3.5"
                                        stroke="currentColor"
                                        fill="none"
                                        d="M18 2.0845 a 15.9155 15.9155 0 0 1 0 31.831 a 15.9155 15.9155 0 0 1 0 -31.831"
                                      />
                                      <path
                                        className={((grokQuota?.weeklyRemainingPct ?? 78) > 20) ? "text-emerald-500" : "text-amber-500"}
                                        strokeDasharray={`${grokQuota?.weeklyRemainingPct ?? 78}, 100`}
                                        strokeWidth="3.5"
                                        strokeLinecap="round"
                                        stroke="currentColor"
                                        fill="none"
                                        d="M18 2.0845 a 15.9155 15.9155 0 0 1 0 31.831 a 15.9155 15.9155 0 0 1 0 -31.831"
                                      />
                                    </svg>
                                  </div>
                                </div>
                              </div>

                              {/* Five Hour Limit Remaining */}
                              <div className="p-3.5 flex items-center justify-between">
                                <div className="flex flex-col gap-0.5">
                                  <span className="font-medium text-white text-xs">Five Hour Limit Remaining</span>
                                  <span className="text-[11px] text-gray-400">
                                    You have used some of your 5-hour limit, it will fully refresh in {grokQuota?.fiveHourRefreshTime || '1 hour, 48 minutes'}.
                                  </span>
                                </div>
                                <div className="flex items-center gap-2.5 shrink-0 ml-4">
                                  <span className="font-semibold text-xs text-white tracking-tight">{grokQuota?.fiveHourRemainingPct ?? 60}%</span>
                                  {/* Circular Progress Ring */}
                                  <div className="relative w-6 h-6 flex items-center justify-center">
                                    <svg className="w-full h-full -rotate-90" viewBox="0 0 36 36">
                                      <path
                                        className="text-[#252834]"
                                        strokeWidth="3.5"
                                        stroke="currentColor"
                                        fill="none"
                                        d="M18 2.0845 a 15.9155 15.9155 0 0 1 0 31.831 a 15.9155 15.9155 0 0 1 0 -31.831"
                                      />
                                      <path
                                        className={((grokQuota?.fiveHourRemainingPct ?? 60) > 20) ? "text-emerald-500" : "text-amber-500"}
                                        strokeDasharray={`${grokQuota?.fiveHourRemainingPct ?? 60}, 100`}
                                        strokeWidth="3.5"
                                        strokeLinecap="round"
                                        stroke="currentColor"
                                        fill="none"
                                        d="M18 2.0845 a 15.9155 15.9155 0 0 1 0 31.831 a 15.9155 15.9155 0 0 1 0 -31.831"
                                      />
                                    </svg>
                                  </div>
                                </div>
                              </div>
                            </div>
                          </div>

                          <div className="flex items-center gap-2">
                            <button
                              onClick={loadGrokAccount}
                              disabled={isSyncingTier}
                              className="flex-1 flex items-center justify-center gap-1.5 px-3 py-2 bg-[#23252f] hover:bg-[#2b2e3a] text-white rounded-lg transition cursor-pointer font-medium text-xs disabled:opacity-50 border border-[#2e323e]"
                              title="Re-query grok.com for latest package status"
                            >
                              <RotateCcw size={13} className={`text-cyan-400 ${isSyncingTier ? 'animate-spin' : ''}`} />
                              <span>{isSyncingTier ? 'Syncing...' : 'Refresh Tier from Grok'}</span>
                            </button>
                            <button
                              onClick={handleLogout}
                              className="px-3.5 py-2 bg-rose-500/15 hover:bg-rose-500/25 text-rose-300 rounded-lg transition cursor-pointer font-medium text-xs border border-rose-800/30"
                            >
                              Log Out
                            </button>
                          </div>
                        </div>
                      )}
                    </div>

                    {/* Remote Cloud GPU / Google Colab (A100 / T4) Card */}
                    <div className="flex flex-col gap-3 bg-[#191a20] p-4 rounded-xl border border-[#292c36]">
                      <div className="flex items-center justify-between">
                        <div className="flex items-center gap-2 font-medium text-white">
                          <Cloud size={16} className="text-cyan-400" />
                          <span>Remote Cloud GPU / Google Colab (A100 / T4)</span>
                        </div>
                        <span className={`px-2.5 py-0.5 rounded text-[10px] font-bold tracking-wider uppercase border ${
                          remoteOllamaStatus.connected
                            ? 'bg-emerald-950 text-emerald-300 border-emerald-700/50 shadow-xs'
                            : remoteOllamaStatus.configured
                            ? 'bg-amber-950 text-amber-300 border-amber-700/50'
                            : 'bg-gray-800 text-gray-400 border-gray-700'
                        }`}>
                          {remoteOllamaStatus.connected ? '🟢 Connected (Cloud GPU)' : remoteOllamaStatus.configured ? '🟡 Connecting...' : '⚪ Local Mode'}
                        </span>
                      </div>

                      <p className="text-[11px] text-gray-400 leading-relaxed">
                        เชื่อมต่อการ์ดจอแรงสูงบนคลาวด์ เช่น <b>Google Colab (A100/T4)</b> เพื่อรันโมเดล <b>Qwen-Coder 32B</b> ข้ามเน็ต โดยไม่กินแรมและสเปกการ์ดจอในคอมของคุณ
                      </p>

                      {/* Input URL and Connect Button */}
                      <div className="flex flex-col gap-2 pt-1">
                        <div className="flex items-center gap-2">
                          <input
                            type="text"
                            value={remoteOllamaInput}
                            onChange={e => setRemoteOllamaInput(e.target.value)}
                            placeholder="วาง Cloudflare URL (เช่น https://xxxx-xxxx.trycloudflare.com)"
                            className="flex-1 bg-[#131418] border border-[#2e313d] rounded-lg px-3 py-2 text-xs text-gray-100 placeholder-gray-500 focus:outline-none focus:border-cyan-500 font-mono select-text"
                          />
                          <button
                            type="button"
                            disabled={isTestingRemote || !remoteOllamaInput.trim()}
                            onClick={handleSaveRemoteOllama}
                            className="px-3.5 py-2 bg-cyan-600 hover:bg-cyan-500 disabled:opacity-40 text-white rounded-lg transition cursor-pointer font-semibold text-xs shrink-0 shadow-sm flex items-center gap-1.5"
                          >
                            {isTestingRemote ? (
                              <>
                                <div className="w-3 h-3 rounded-full border-2 border-white border-t-transparent animate-spin" />
                                <span>กำลังเชื่อมต่อ...</span>
                              </>
                            ) : (
                              <>
                                <Check size={13} />
                                <span>เชื่อมต่อ</span>
                              </>
                            )}
                          </button>

                          {remoteOllamaStatus.configured && (
                            <button
                              type="button"
                              onClick={handleDisconnectRemoteOllama}
                              disabled={isTestingRemote}
                              className="px-2.5 py-2 bg-[#252834] hover:bg-[#2d3140] text-gray-300 rounded-lg transition cursor-pointer text-xs"
                              title="ยกเลิกการเชื่อมต่อและสลับกลับมาใช้ Local Ollama"
                            >
                              ตัดการเชื่อมต่อ
                            </button>
                          )}
                        </div>

                        {remoteSaveMsg && (
                          <span className={`text-[11px] font-medium ${remoteOllamaStatus.connected ? 'text-emerald-400' : 'text-amber-400'}`}>
                            {remoteSaveMsg}
                          </span>
                        )}

                        {remoteOllamaStatus.connected && remoteOllamaStatus.models && remoteOllamaStatus.models.length > 0 && (
                          <div className="bg-[#121317] border border-[#23252f] rounded-lg p-2 text-xs flex flex-wrap gap-1.5 items-center">
                            <span className="text-gray-400 text-[10.5px]">โมเดลที่พบบน Cloud:</span>
                            {remoteOllamaStatus.models.map((m, idx) => (
                              <span key={idx} className="px-2 py-0.5 rounded bg-cyan-950/70 border border-cyan-800/40 text-cyan-300 font-mono text-[10px]">
                                {m}
                              </span>
                            ))}
                          </div>
                        )}
                      </div>

                      {/* Expandable Colab Script Guide */}
                      <div className="border-t border-[#252834] pt-2 mt-1">
                        <button
                          type="button"
                          onClick={() => setShowColabGuide(prev => !prev)}
                          className="flex items-center gap-1.5 text-xs text-cyan-400 hover:text-cyan-300 transition cursor-pointer font-medium"
                        >
                          <Terminal size={13} />
                          <span>{showColabGuide ? 'ซ่อนโค้ดสำหรับรันบน Google Colab' : '📋 ดูโค้ดสคริปต์ 1 คลิกสำหรับรันบน Google Colab (A100)'}</span>
                        </button>

                        {showColabGuide && (
                          <div className="mt-2.5 bg-[#121316] border border-[#242630] rounded-xl p-3 flex flex-col gap-2.5">
                            <div className="flex items-center justify-between">
                              <span className="text-[11px] text-gray-300 font-medium">
                                วิธีใช้: เปิด Google Colab ➔ เลือก GPU A100 ➔ วางโค้ดนี้แล้วกด Run (Play):
                              </span>
                              <button
                                type="button"
                                onClick={() => {
                                  copyToClipboard(colabPythonScript);
                                  setCopiedColabCode(true);
                                  setTimeout(() => setCopiedColabCode(false), 2500);
                                }}
                                className="flex items-center gap-1.5 px-2.5 py-1 bg-cyan-600/20 hover:bg-cyan-600/30 text-cyan-300 border border-cyan-700/40 rounded-md text-[11px] transition cursor-pointer font-medium"
                              >
                                {copiedColabCode ? (
                                  <>
                                    <Check size={12} className="text-emerald-400" />
                                    <span className="text-emerald-400">คัดลอกโค้ดแล้ว!</span>
                                  </>
                                ) : (
                                  <>
                                    <Copy size={12} />
                                    <span>คัดลอกสคริปต์</span>
                                  </>
                                )}
                              </button>
                            </div>
                            <pre className="p-2.5 bg-[#0e0f12] border border-[#1e2028] rounded-lg text-gray-300 font-mono text-[10.5px] overflow-x-auto whitespace-pre leading-relaxed select-text cursor-text max-h-48">
                              {colabPythonScript}
                            </pre>
                          </div>
                        )}
                      </div>
                    </div>

                    {/* Local Engine (private Ollama sidecar) */}
                    <div className="flex flex-col gap-2.5 bg-[#191a20] p-4 rounded-xl border border-[#292c36]">
                      <div className="flex items-center justify-between">
                        <div className="flex items-center gap-2 font-medium text-white">
                          <HardDrive size={16} className="text-emerald-400" />
                          <span>Local Engine</span>
                        </div>
                        <span className={`px-2 py-0.5 rounded text-[10px] border ${
                          engine?.installed
                            ? 'bg-emerald-950 text-emerald-400 border-emerald-800/40'
                            : 'bg-gray-800 text-gray-400 border-gray-700'
                        }`}>
                          {engine?.installed ? (engine.running ? 'Running' : 'Installed') : 'Not installed'}
                        </span>
                      </div>
                      <p className="text-[11px] text-gray-400 leading-relaxed">
                        Runs models natively on your GPU/CPU, fully isolated inside AH-Code. One-time download of about 1.4 GB, needed before any local model.
                      </p>

                      {engineProgress && engineProgress.stage !== 'error' && engineProgress.stage !== 'cancelled' && (
                        <div className="flex flex-col gap-1.5">
                          <div className="h-1.5 rounded-full bg-[#252834] overflow-hidden">
                            <div className="h-full bg-emerald-500 transition-all duration-150" style={{ width: `${Math.min(100, engineProgress.percent)}%` }} />
                          </div>
                          <div className="flex items-center justify-between text-[10.5px] text-gray-400 font-mono">
                            <span>{engineProgress.message}{engineProgress.total > 0 && engineProgress.stage === 'downloading' ? ` ${formatBytes(engineProgress.done)} / ${formatBytes(engineProgress.total)}` : ''}</span>
                            <span>{Math.round(engineProgress.percent)}%</span>
                          </div>
                        </div>
                      )}
                      {engineProgress && (engineProgress.stage === 'error' || engineProgress.stage === 'cancelled') && (
                        <p className="text-[11px] text-rose-300 font-mono">{engineProgress.message}</p>
                      )}

                      {engine && !engine.supported ? (
                        <p className="text-[11px] text-amber-300">Local models are available on Windows for now. Linux and macOS are coming.</p>
                      ) : engine?.installed ? (
                        <div className="flex items-center gap-2">
                          <button
                            onClick={async () => {
                              if (window.confirm('Remove the local engine? Downloaded models stay on disk.')) {
                                await UninstallEngine();
                                refreshModels();
                              }
                            }}
                            className="px-3 py-1.5 bg-[#23252f] hover:bg-[#2b2e3a] text-gray-300 rounded-lg transition cursor-pointer text-[11px] border border-[#2e323e]"
                          >
                            Remove engine
                          </button>
                        </div>
                      ) : engine?.installing || (engineProgress && !['error', 'cancelled', 'done'].includes(engineProgress.stage)) ? (
                        <button
                          onClick={() => CancelEngineInstall()}
                          className="px-3 py-1.5 bg-rose-500/15 hover:bg-rose-500/25 text-rose-300 rounded-lg transition cursor-pointer text-[11px] border border-rose-800/30 self-start"
                        >
                          Cancel
                        </button>
                      ) : (
                        <button
                          onClick={handleInstallEngine}
                          className="flex items-center justify-center gap-1.5 px-3 py-2 bg-emerald-500 hover:bg-emerald-400 text-gray-950 rounded-lg transition cursor-pointer font-semibold text-xs self-start"
                        >
                          <Download size={13} />
                          <span>Install Local Engine</span>
                        </button>
                      )}
                    </div>

                    {/* Model registry */}
                    <div className="flex flex-col gap-2.5 bg-[#191a20] p-4 rounded-xl border border-[#292c36]">
                      <div className="flex items-center justify-between">
                        <div className="flex items-center gap-2 font-medium text-white">
                          <Cpu size={16} className="text-emerald-400" />
                          <span>Local Models & Agents</span>
                        </div>
                        <button
                          onClick={() => setAddModelOpen(o => !o)}
                          className="flex items-center gap-1 px-2.5 py-1 bg-[#23252f] hover:bg-[#2b2e3a] text-gray-200 rounded-lg transition cursor-pointer text-[11px] border border-[#2e323e]"
                        >
                          <Plus size={12} />
                          <span>Add Model</span>
                        </button>
                      </div>

                      {/* AI Vision Eyes Configuration Card */}
                      {(() => {
                        const localEyes = models.find(m => m.supportsVision && m.installed && m.kind === 'ollama');
                        const isCloudVisionReady = !!visionStatus?.hasCloudKey;
                        const isLocalVisionReady = !!localEyes;
                        const isVisionActive = isCloudVisionReady || isLocalVisionReady;
                        const moondreamModel = models.find(m => m.id === 'moondream-vision');
                        const qwenVlModel = models.find(m => m.id === 'qwen-vl-3b');

                        return (
                          <div className={`flex flex-col gap-3 rounded-xl p-4 border text-xs ${
                            isVisionActive
                              ? 'bg-purple-950/20 border-purple-500/30 text-purple-200'
                              : 'bg-amber-950/20 border-amber-500/30 text-amber-200'
                          }`}>
                            <div className="flex items-center justify-between">
                              <div className="flex items-center gap-2">
                                <Eye size={16} className={isVisionActive ? 'text-purple-400' : 'text-amber-400'} />
                                <span className="font-semibold text-white text-xs">
                                  AI Vision Eyes (ดวงตาเอไอ — อ่านสกรีนช็อต & UI Mockup)
                                </span>
                              </div>
                              <span className={`px-2 py-0.5 rounded text-[10px] font-bold border ${
                                isVisionActive
                                  ? 'bg-purple-950 text-purple-300 border-purple-800/40'
                                  : 'bg-amber-950 text-amber-300 border-amber-800/40'
                              }`}>
                                {isVisionActive
                                  ? (isCloudVisionReady ? `Cloud Ready (${visionStatus?.activeProvider?.toUpperCase()})` : `Local Ready (${localEyes?.name})`)
                                  : 'ต้องการตั้งค่า (Not Ready)'}
                              </span>
                            </div>

                            <p className="text-[11px] text-gray-300 leading-relaxed">
                              {isVisionActive
                                ? `พร้อมใช้งาน: เมื่อคุณกด Ctrl+V หรือแนบภาพหน้าจอ ระบบดวงตา AI (${isCloudVisionReady ? 'Cloud Groq' : localEyes?.name}) จะแปลงองค์ประกอบ สี ข้อความ และ UI เป็นสเปกโค้ดให้โมเดลเขียนตามได้ทันที!`
                                : 'ยังไม่ได้เชื่อมต่อดวงตา: โมเดลโค้ด (Qwen-Coder 7B) จะยังมองไม่เห็นภาพหน้าจอจนกว่าจะตั้งค่าอย่างใดอย่างหนึ่งด้านล่างนี้'}
                            </p>

                            {/* Option 1: Fast Cloud Vision Key */}
                            <div className="bg-[#121317] border border-[#2b2d38] rounded-lg p-3 flex flex-col gap-2">
                              <div className="flex items-center justify-between">
                                <span className="font-medium text-white text-[11.5px] flex items-center gap-1.5">
                                  <Zap size={13} className="text-amber-400" />
                                  <span>วิธีที่ 1: เชื่อมต่อ Cloud Vision (เร็วจัด 0.3 วิ / ฟรี 14,400 ครั้ง/วัน)</span>
                                </span>
                                {visionStatus?.hasGroqKey && (
                                  <span className="text-[10px] text-emerald-400 flex items-center gap-1 font-mono">
                                    <Check size={11} /> คีย์เชื่อมต่ออยู่
                                  </span>
                                )}
                              </div>
                              <p className="text-[10.5px] text-gray-400">
                                ใช้งานผ่าน Groq Cloud API — ประมวลผลภาพได้ทันทีโดยไม่เปลือง VRAM เครื่อง
                                <button
                                  type="button"
                                  onClick={() => BrowserOpenURL('https://console.groq.com/keys')}
                                  className="text-cyan-400 hover:underline ml-1 cursor-pointer"
                                >
                                  (กดรับ Groq API Key ฟรีที่นี่)
                                </button>
                              </p>
                              <div className="flex items-center gap-2">
                                <input
                                  type="password"
                                  value={groqKeyInput}
                                  onChange={e => setGroqKeyInput(e.target.value)}
                                  placeholder={visionStatus?.hasGroqKey ? `Groq Key: ${visionStatus.groqApiKey || '••••••••'}` : 'วาง Groq API Key (gsk_...)'}
                                  className="flex-1 bg-[#191a21] border border-[#303340] rounded-md px-2.5 py-1.5 text-xs text-gray-100 placeholder-gray-500 focus:outline-none focus:border-cyan-500 font-mono select-text"
                                />
                                <button
                                  type="button"
                                  disabled={!groqKeyInput.trim() || isSavingVision}
                                  onClick={handleSaveVisionConfig}
                                  className="px-3 py-1.5 bg-cyan-600 hover:bg-cyan-500 disabled:opacity-40 text-white rounded-md transition cursor-pointer font-medium text-xs shrink-0"
                                >
                                  {isSavingVision ? 'กำลังบันทึก...' : 'บันทึกคีย์'}
                                </button>
                              </div>
                              {visionSaveMsg && (
                                <span className="text-[10.5px] text-emerald-400 font-medium">{visionSaveMsg}</span>
                              )}
                            </div>

                            {/* Option 2: Local Vision Models */}
                            <div className="bg-[#121317] border border-[#2b2d38] rounded-lg p-3 flex flex-col gap-2">
                              <span className="font-medium text-white text-[11.5px] flex items-center gap-1.5">
                                <HardDrive size={13} className="text-emerald-400" />
                                <span>วิธีที่ 2: ติดตั้งโมเดลสายตาในเครื่อง (ออฟไลน์ 100% ส่วนตัว ไม่ต่อเน็ต)</span>
                              </span>
                              <div className="grid grid-cols-2 gap-2 pt-1">
                                {/* Moondream2 */}
                                <div className="p-2.5 rounded-lg bg-[#181921] border border-[#2c2f3c] flex flex-col justify-between gap-1.5">
                                  <div>
                                    <div className="flex items-center justify-between">
                                      <span className="font-bold text-xs text-white">Moondream2</span>
                                      <span className="text-[9px] px-1.5 py-0.2 rounded bg-cyan-950 text-cyan-400 border border-cyan-800/40">~800 MB</span>
                                    </div>
                                    <p className="text-[10px] text-gray-400 mt-0.5">เบาพิเศษ ทำงานไวมาก เหมาะกับ RTX 3050 Ti</p>
                                  </div>
                                  {moondreamModel?.installed ? (
                                    <span className="text-[10px] text-emerald-400 font-semibold flex items-center gap-1">
                                      <Check size={12} /> ติดตั้งแล้ว (พร้อมใช้งาน)
                                    </span>
                                  ) : modelProgress['moondream-vision'] ? (
                                    <span className="text-[10px] text-cyan-400 animate-pulse font-mono">
                                      กำลังดาวน์โหลด {Math.round(modelProgress['moondream-vision'].percent)}%
                                    </span>
                                  ) : (
                                    <button
                                      type="button"
                                      onClick={() => handleCheckAndInstall('moondream-vision')}
                                      className="w-full py-1 rounded bg-emerald-500/20 hover:bg-emerald-500/30 text-emerald-300 border border-emerald-500/30 text-[10.5px] font-medium transition cursor-pointer flex items-center justify-center gap-1"
                                    >
                                      <Download size={11} />
                                      <span>ติดตั้ง Moondream2</span>
                                    </button>
                                  )}
                                </div>

                                {/* Qwen2.5-VL 3B */}
                                <div className="p-2.5 rounded-lg bg-[#181921] border border-[#2c2f3c] flex flex-col justify-between gap-1.5">
                                  <div>
                                    <div className="flex items-center justify-between">
                                      <span className="font-bold text-xs text-white">Qwen2.5-VL 3B</span>
                                      <span className="text-[9px] px-1.5 py-0.2 rounded bg-purple-950 text-purple-400 border border-purple-800/40">~1.9 GB</span>
                                    </div>
                                    <p className="text-[10px] text-gray-400 mt-0.5">สายตา Alibaba แม่นยำเรื่องโค้ด ไดอะแกรม และ UI</p>
                                  </div>
                                  {qwenVlModel?.installed ? (
                                    <span className="text-[10px] text-purple-300 font-semibold flex items-center gap-1">
                                      <Check size={12} /> ติดตั้งแล้ว (พร้อมใช้งาน)
                                    </span>
                                  ) : modelProgress['qwen-vl-3b'] ? (
                                    <span className="text-[10px] text-purple-400 animate-pulse font-mono">
                                      กำลังดาวน์โหลด {Math.round(modelProgress['qwen-vl-3b'].percent)}%
                                    </span>
                                  ) : (
                                    <button
                                      type="button"
                                      onClick={() => handleCheckAndInstall('qwen-vl-3b')}
                                      className="w-full py-1 rounded bg-purple-500/20 hover:bg-purple-500/30 text-purple-300 border border-purple-500/30 text-[10.5px] font-medium transition cursor-pointer flex items-center justify-center gap-1"
                                    >
                                      <Download size={11} />
                                      <span>ติดตั้ง Qwen-VL 3B</span>
                                    </button>
                                  )}
                                </div>
                              </div>
                            </div>
                          </div>
                        );
                      })()}

                      {modelError && (
                        <p className="text-[11px] text-rose-300 bg-rose-950/30 border border-rose-500/30 rounded-lg px-3 py-2 font-mono">{modelError}</p>
                      )}

                      {addModelOpen && (
                        <div className="flex flex-col gap-2 bg-[#131418] border border-[#242630] rounded-lg p-3">
                          <div className="flex items-center gap-1.5 text-[11px]">
                            {[['ollama', 'Download a model'], ['cli', 'Terminal AI agent']].map(([k, label]) => (
                              <button
                                key={k}
                                onClick={() => setNewModel(n => ({ ...n, kind: k }))}
                                className={`px-2.5 py-1 rounded-md transition cursor-pointer border ${newModel.kind === k ? 'bg-emerald-950/70 text-emerald-300 border-emerald-800/40' : 'bg-[#1a1c22] text-gray-400 border-[#2a2d36] hover:text-gray-200'}`}
                              >
                                {label}
                              </button>
                            ))}
                          </div>
                          <input
                            value={newModel.name}
                            onChange={e => setNewModel(n => ({ ...n, name: e.target.value }))}
                            placeholder="Display name (e.g. DeepSeek Coder)"
                            className="bg-[#0f1013] border border-[#2a2d36] rounded-md px-2.5 py-1.5 text-xs text-gray-100 placeholder-gray-500 focus:outline-none focus:border-emerald-500/50 select-text"
                          />
                          {newModel.kind === 'ollama' ? (
                            <>
                              <input
                                value={newModel.tag}
                                onChange={e => setNewModel(n => ({ ...n, tag: e.target.value }))}
                                placeholder="Model tag from ollama.com/library (e.g. qwen2.5-coder:14b)"
                                className="bg-[#0f1013] border border-[#2a2d36] rounded-md px-2.5 py-1.5 text-xs text-gray-100 placeholder-gray-500 focus:outline-none focus:border-emerald-500/50 font-mono select-text"
                              />
                              <label className="flex items-center gap-2 text-[11px] text-gray-300 cursor-pointer">
                                <input
                                  type="checkbox"
                                  checked={newModel.vision}
                                  onChange={e => setNewModel(n => ({ ...n, vision: e.target.checked }))}
                                  className="accent-emerald-500"
                                />
                                <span>This model can see images (use it as the "eyes")</span>
                              </label>
                              <p className="text-[10.5px] text-gray-500">Weights are downloaded through the Local Engine. Check the size on the model page first; 7B models need about 5 GB.</p>
                            </>
                          ) : (
                            <>
                              <input
                                value={newModel.command}
                                onChange={e => setNewModel(n => ({ ...n, command: e.target.value }))}
                                placeholder="Command to run (e.g. gemini, or full path to .exe)"
                                className="bg-[#0f1013] border border-[#2a2d36] rounded-md px-2.5 py-1.5 text-xs text-gray-100 placeholder-gray-500 focus:outline-none focus:border-emerald-500/50 font-mono select-text"
                              />
                              <textarea
                                value={newModel.args}
                                onChange={e => setNewModel(n => ({ ...n, args: e.target.value }))}
                                placeholder={'Arguments, one per line. Use {prompt} where the message goes:\n-p\n{prompt}'}
                                rows={3}
                                className="bg-[#0f1013] border border-[#2a2d36] rounded-md px-2.5 py-1.5 text-xs text-gray-100 placeholder-gray-500 focus:outline-none focus:border-emerald-500/50 font-mono resize-none select-text"
                              />
                              <p className="text-[10.5px] text-gray-500">Runs inside your project folder. Without {'{prompt}'} the message is sent on stdin. The agent must already be installed on this PC.</p>
                            </>
                          )}
                          <div className="flex items-center gap-2 pt-0.5">
                            <button
                              onClick={handleAddModel}
                              className="px-3 py-1.5 bg-emerald-500 hover:bg-emerald-400 text-gray-950 rounded-lg transition cursor-pointer font-semibold text-xs"
                            >
                              Add
                            </button>
                            <button
                              onClick={() => { setAddModelOpen(false); setModelError(''); }}
                              className="px-3 py-1.5 text-gray-400 hover:text-gray-200 transition cursor-pointer text-xs"
                            >
                              Cancel
                            </button>
                          </div>
                        </div>
                      )}

                      <div className="flex flex-col divide-y divide-[#1e202a] bg-[#121317] border border-[#252834] rounded-xl overflow-hidden">
                        {models.map(m => {
                          const prog = modelProgress[m.id];
                          const busy = !!prog && !['done', 'error', 'cancelled'].includes(prog.stage);
                          return (
                            <div key={m.id} className="p-3 flex flex-col gap-2">
                              <div className="flex items-center justify-between gap-3">
                                <div className="flex items-center gap-2.5 min-w-0">
                                  {m.kind === 'cli'
                                    ? <Terminal size={15} className="text-emerald-400 shrink-0" />
                                    : <Cpu size={15} className="text-emerald-400 shrink-0" />}
                                  <div className="flex flex-col min-w-0">
                                    <span className="font-medium text-white text-xs truncate">{m.name}</span>
                                    <span className="text-[10.5px] text-gray-500 truncate font-mono">
                                      {m.kind === 'cli' ? `${m.command}${m.args && m.args.length ? ' ' + m.args.join(' ') : ''}` : `${m.ollamaTag}${m.sizeHint ? ` · ${m.sizeHint}` : ''}`}
                                    </span>
                                  </div>
                                </div>
                                <div className="flex items-center gap-1.5 shrink-0">
                                  {m.installed ? (
                                    <span className="px-2 py-0.5 rounded text-[10px] bg-emerald-950 text-emerald-400 border border-emerald-800/40">Ready</span>
                                  ) : m.kind === 'cli' ? (
                                    <span className="px-2 py-0.5 rounded text-[10px] bg-amber-950/60 text-amber-300 border border-amber-800/40">Command not found</span>
                                  ) : busy ? (
                                    <button
                                      onClick={() => CancelModelInstall(m.id)}
                                      className="px-2.5 py-1 bg-rose-500/15 hover:bg-rose-500/25 text-rose-300 rounded-lg transition cursor-pointer text-[11px] border border-rose-800/30"
                                    >
                                      Pause
                                    </button>
                                  ) : (
                                    <button
                                      onClick={() => handleInstallModel(m.id)}
                                      disabled={!engine?.installed}
                                      title={engine?.installed ? '' : 'Install the Local Engine first'}
                                      className="flex items-center gap-1 px-2.5 py-1 bg-emerald-500 hover:bg-emerald-400 disabled:opacity-40 disabled:hover:bg-emerald-500 text-gray-950 rounded-lg transition cursor-pointer font-semibold text-[11px]"
                                    >
                                      <Download size={11} />
                                      <span>{prog?.stage === 'cancelled' ? 'Resume' : 'Install'}</span>
                                    </button>
                                  )}
                                  {m.installed && m.kind === 'ollama' && (
                                    <button
                                      onClick={() => handleUninstallModel(m)}
                                      className="p-1 text-gray-500 hover:text-rose-400 rounded transition cursor-pointer"
                                      title="Delete downloaded files"
                                    >
                                      <Trash2 size={13} />
                                    </button>
                                  )}
                                  {!m.builtin && !busy && (
                                    <button
                                      onClick={() => handleRemoveModel(m)}
                                      className="p-1 text-gray-500 hover:text-rose-400 rounded transition cursor-pointer"
                                      title="Remove from list"
                                    >
                                      <X size={13} />
                                    </button>
                                  )}
                                </div>
                              </div>

                              {prog && prog.stage !== 'done' && (
                                prog.stage === 'error' ? (
                                  <p className="text-[11px] text-rose-300 font-mono">{prog.message}</p>
                                ) : prog.stage === 'cancelled' ? (
                                  <p className="text-[11px] text-gray-400">Download paused. Resume continues from where it stopped.</p>
                                ) : (
                                  <div className="flex flex-col gap-1.5">
                                    <div className="h-1.5 rounded-full bg-[#252834] overflow-hidden">
                                      <div className="h-full bg-emerald-500 transition-all duration-150" style={{ width: `${Math.min(100, prog.percent)}%` }} />
                                    </div>
                                    <div className="flex items-center justify-between text-[10.5px] text-gray-400 font-mono">
                                      <span>{prog.message}{prog.total > 0 && prog.stage === 'pulling' ? ` ${formatBytes(prog.done)} / ${formatBytes(prog.total)}` : ''}</span>
                                      <span>{Math.round(prog.percent)}%</span>
                                    </div>
                                  </div>
                                )
                              )}
                            </div>
                          );
                        })}
                      </div>
                    </div>

                    {/* Quota & Gatekeeper */}
                    <div className="flex flex-col gap-2.5 bg-[#191a20] p-4 rounded-xl border border-[#292c36]">
                      <div className="flex items-center justify-between">
                        <div className="flex items-center gap-2 font-medium text-white">
                          <ShieldCheck size={16} className="text-cyan-400" />
                          <span>Token Quota & Remote Gatekeeper</span>
                        </div>
                        <span className="font-mono text-cyan-400 font-bold">{tokensRemaining} Tokens</span>
                      </div>
                      <div className="flex items-center gap-2 mt-1">
                        <button
                          onClick={() => handleRefill(100)}
                          className="flex-1 py-1.5 bg-cyan-500 hover:bg-cyan-400 text-gray-950 font-semibold rounded-lg transition cursor-pointer text-center"
                        >
                          Refill 100 Tokens
                        </button>
                        <button
                          onClick={() => handleRefill(-tokensRemaining)}
                          className="flex-1 py-1.5 bg-rose-500/20 hover:bg-rose-500/30 text-rose-300 font-medium rounded-lg transition cursor-pointer text-center border border-rose-800/30"
                        >
                          Simulate 0 Tokens (Test)
                        </button>
                      </div>
                    </div>

                    {/* Network Gateway */}
                    <div className="flex items-center justify-between px-1">
                      <div className="flex flex-col">
                        <span className="font-medium text-white">Simulate Network Gateway</span>
                        <span className="text-[11px] text-gray-400">Toggle online status to test generic error masking (502)</span>
                      </div>
                      <button
                        onClick={toggleOnline}
                        className={`px-3 py-1 rounded-lg text-xs font-semibold transition cursor-pointer ${isOnline ? 'bg-emerald-500/20 text-emerald-300 border border-emerald-800/40' : 'bg-rose-500/20 text-rose-300 border border-rose-800/40'}`}
                      >
                        {isOnline ? 'Online (Active)' : 'Offline (Simulated)'}
                      </button>
                    </div>
                  </div>
                )}

                {/* 3. APPLICATION TAB */}
                {settingsTab === 'application' && (
                  <div className="flex flex-col gap-4">
                    <div className="flex items-center justify-between py-2 border-b border-[#1e2027]">
                      <div className="flex flex-col">
                        <span className="font-medium text-white">Project Workspace</span>
                        <span className="text-[11px] text-gray-400 font-mono truncate max-w-sm">{projectFolder}</span>
                      </div>
                      <button
                        onClick={handleSelectFolder}
                        className="px-3 py-1.5 bg-[#20222a] hover:bg-[#282b35] text-white rounded-lg transition cursor-pointer border border-[#2b2d38]"
                      >
                        Change Directory
                      </button>
                    </div>

                    <div className="flex items-center justify-between py-2 border-b border-[#1e2027]">
                      <div className="flex flex-col">
                        <span className="font-medium text-white">Grok Engine Binary</span>
                        <span className="text-[11px] text-gray-400 font-mono">~/.grok/bin/grok.exe (v1.0.41 Stable)</span>
                      </div>
                      <span className="px-2 py-0.5 rounded text-[10px] bg-emerald-950 text-emerald-400 border border-emerald-800/40 font-mono">
                        Active
                      </span>
                    </div>

                    <div className="flex items-center justify-between py-2 border-b border-[#1e2027]">
                      <div className="flex flex-col">
                        <span className="font-medium text-white">Terminal Subprocess Policy</span>
                        <span className="text-[11px] text-gray-400">Windows Silent Background Execution (HideWindow 0x08000000)</span>
                      </div>
                      <span className="text-gray-300 font-mono text-[11px]">Enabled</span>
                    </div>
                  </div>
                )}

                {/* 4. APPEARANCE TAB */}
                {settingsTab === 'appearance' && (
                  <div className="flex flex-col gap-4">
                    <div className="flex items-center justify-between py-2 border-b border-[#1e2027]">
                      <div className="flex flex-col">
                        <span className="font-medium text-white">Theme</span>
                        <span className="text-[11px] text-gray-400">Select the visual skin for AH-Code IDE.</span>
                      </div>
                      <select className="bg-[#1b1c24] border border-[#2b2d38] text-white rounded-lg px-3 py-1.5 text-xs focus:outline-none focus:border-cyan-500 cursor-pointer">
                        <option value="antigravity_dark">Antigravity Dark (Active)</option>
                        <option value="midnight">Obsidian Midnight</option>
                        <option value="oled">Pure OLED Black</option>
                      </select>
                    </div>

                    <div className="flex items-center justify-between py-2 border-b border-[#1e2027]">
                      <div className="flex flex-col">
                        <span className="font-medium text-white">Code Typography</span>
                        <span className="text-[11px] text-gray-400">Monospace font family used in code views and thoughts.</span>
                      </div>
                      <span className="text-gray-300 font-mono text-xs">JetBrains Mono / Cascadia Code</span>
                    </div>
                  </div>
                )}

                {/* 5. CUSTOMIZATIONS TAB */}
                {settingsTab === 'customizations' && (
                  <div className="flex flex-col gap-3">
                    <span className="font-medium text-white">Installed Skills & Rules</span>
                    <div className="grid grid-cols-2 gap-2.5 pt-1">
                      <div className="bg-[#191a20] p-3 rounded-xl border border-[#282a34] flex flex-col gap-1">
                        <div className="flex items-center justify-between">
                          <span className="font-semibold text-white">antigravity-guide</span>
                          <span className="text-[9px] px-1.5 py-0.2 rounded bg-cyan-950 text-cyan-400 border border-cyan-800/40">Active</span>
                        </div>
                        <span className="text-[10px] text-gray-400">AGY workflows, keybindings, and sidecar reference.</span>
                      </div>
                      <div className="bg-[#191a20] p-3 rounded-xl border border-[#282a34] flex flex-col gap-1">
                        <div className="flex items-center justify-between">
                          <span className="font-semibold text-white">generative_ui</span>
                          <span className="text-[9px] px-1.5 py-0.2 rounded bg-cyan-950 text-cyan-400 border border-cyan-800/40">Active</span>
                        </div>
                        <span className="text-[10px] text-gray-400">Interactive inline HTML and diagram rendering.</span>
                      </div>
                    </div>
                  </div>
                )}

                {/* 6. BROWSER TAB */}
                {settingsTab === 'browser' && (
                  <div className="flex flex-col gap-4">
                    <div className="flex items-center justify-between py-2 border-b border-[#1e2027]">
                      <div className="flex flex-col">
                        <span className="font-medium text-white">Web Search Provider</span>
                        <span className="text-[11px] text-gray-400">Engine used when agent searches documentation or web.</span>
                      </div>
                      <span className="text-cyan-400 font-semibold text-xs">Grok Live Web Search</span>
                    </div>
                    <div className="flex items-center justify-between py-2 border-b border-[#1e2027]">
                      <div className="flex flex-col">
                        <span className="font-medium text-white">Headless Browser Engine</span>
                        <span className="text-[11px] text-gray-400">Microsoft Edge WebView2 Embedded</span>
                      </div>
                      <span className="text-emerald-400 font-semibold text-xs">Installed</span>
                    </div>
                  </div>
                )}
              </div>
            </div>
          </div>
        </div>
      )}

      {/* 5. REAL SUPERGROK OAUTH MODAL */}
      {loginModalOpen && (
        <div className="fixed inset-0 bg-black/80 backdrop-blur-xs flex items-center justify-center z-50 p-4">
          <div className="bg-[#18191d] border border-[#2b2d35] rounded-2xl w-full max-w-md shadow-2xl p-6 flex flex-col gap-4 text-center animate-in fade-in zoom-in-95 duration-200">
            <div className="w-12 h-12 rounded-2xl bg-cyan-950 border border-cyan-800/40 text-cyan-400 mx-auto flex items-center justify-center shadow-lg shadow-cyan-500/10">
              <Sparkles size={24} />
            </div>

            <div className="flex flex-col gap-1">
              <h3 className="text-base font-semibold text-white">Log in with SuperGrok</h3>
              <p className="text-xs text-gray-400 leading-relaxed">
                Browser has opened to <span className="text-cyan-400 font-mono">accounts.x.ai</span>. Confirm the device code below to authenticate your account.
              </p>
            </div>

            {loginSession?.userCode ? (
              <div className="bg-[#121316] border border-[#26282f] rounded-xl p-4 flex flex-col gap-2">
                <span className="text-[11px] text-gray-400 font-medium uppercase tracking-wider">Your Device Code</span>
                <div className="flex items-center justify-center gap-2">
                  <span className="text-2xl font-mono font-bold tracking-widest text-cyan-300">
                    {loginSession.userCode}
                  </span>
                  <button
                    onClick={() => navigator.clipboard.writeText(loginSession.userCode)}
                    className="p-1.5 hover:bg-[#252830] rounded-lg text-gray-400 hover:text-white transition cursor-pointer"
                    title="Copy Code"
                  >
                    <Copy size={16} />
                  </button>
                </div>
              </div>
            ) : (
              <div className="py-4 text-xs text-gray-400 flex items-center justify-center gap-2">
                <span className="w-2 h-2 rounded-full bg-cyan-400 animate-ping"></span>
                <span>Requesting authorization code...</span>
              </div>
            )}

            {loginSuccessMessage ? (
              <div className="flex items-center justify-center gap-2 text-xs text-emerald-400 bg-emerald-950/40 border border-emerald-800/40 py-2 rounded-xl">
                <CheckCircle2 size={16} />
                <span>{loginSuccessMessage}</span>
              </div>
            ) : (
              <div className="flex items-center justify-center gap-2 text-xs text-gray-400 pt-1">
                <span className="w-1.5 h-1.5 rounded-full bg-cyan-400 animate-pulse"></span>
                <span>Waiting for confirmation in browser...</span>
              </div>
            )}

            <div className="flex items-center gap-2 pt-2">
              {loginSession?.authUrl && (
                <button
                  onClick={() => BrowserOpenURL(loginSession.authUrl)}
                  className="flex-1 flex items-center justify-center gap-1.5 py-2 rounded-lg bg-[#252830] hover:bg-[#2d303a] text-xs font-medium text-gray-200 transition cursor-pointer"
                >
                  <ExternalLink size={13} />
                  <span>Open Browser Again</span>
                </button>
              )}
              <button
                onClick={() => {
                  CancelGrokLogin();
                  setLoginModalOpen(false);
                }}
                className="flex-1 py-2 rounded-lg bg-transparent hover:bg-[#252830] text-xs font-medium text-gray-400 hover:text-white transition cursor-pointer border border-[#2b2d35]"
              >
                Cancel
              </button>
            </div>
          </div>
        </div>
      )}

      {/* 4. GATEWAY USER ACCOUNT MODAL */}
      {isAuthModalOpen && (
        <div className="fixed inset-0 bg-black/75 backdrop-blur-sm z-50 flex items-center justify-center p-4">
          <div className="bg-[#141519] border border-[#242630] rounded-2xl w-full max-w-sm p-6 shadow-2xl flex flex-col gap-5 text-gray-200 animate-in fade-in zoom-in-95 duration-150">
            {/* Header */}
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-2.5">
                <div className="w-8 h-8 rounded-lg bg-gradient-to-tr from-cyan-500 to-emerald-400 text-gray-950 flex items-center justify-center font-bold text-sm shadow-md">
                  ⚡
                </div>
                <div>
                  <h3 className="font-bold text-white text-sm">AH-Code Account</h3>
                  <p className="text-[11px] text-gray-400">Web Gateway & Token Sync</p>
                </div>
              </div>
              <button
                onClick={() => setIsAuthModalOpen(false)}
                className="text-gray-500 hover:text-white p-1 rounded-lg hover:bg-[#20222a] transition cursor-pointer"
              >
                <X size={16} />
              </button>
            </div>

            {gatewayUser ? (
              /* Logged In View */
              <div className="flex flex-col gap-4">
                <div className="bg-[#191b22] border border-[#262834] rounded-xl p-4 flex items-center gap-3">
                  <div className="w-11 h-11 rounded-xl bg-gradient-to-tr from-cyan-500 to-emerald-400 text-gray-950 font-bold text-sm flex items-center justify-center shrink-0">
                    {gatewayUser.initials || 'U'}
                  </div>
                  <div className="min-w-0 flex-1">
                    <div className="text-sm font-semibold text-white truncate">{gatewayUser.name}</div>
                    <div className="text-xs text-gray-400 truncate">{gatewayUser.email}</div>
                    <div className="flex items-center gap-2 mt-1">
                      <span className={`px-2 py-0.5 rounded text-[10px] font-bold uppercase ${
                        gatewayUser.plan === 'unlimited' ? 'bg-amber-950 text-amber-300 border border-amber-800/40' : 'bg-cyan-950 text-cyan-300 border border-cyan-800/40'
                      }`}>
                        {gatewayUser.plan || 'Free'} Plan
                      </span>
                      <span className="text-[11px] text-cyan-300 font-mono font-semibold">
                        {tokensRemaining} Tokens
                      </span>
                    </div>
                  </div>
                </div>

                <div className="flex items-center justify-between text-xs text-gray-400 px-1">
                  <span>Gateway Host:</span>
                  <span className="text-gray-300 font-mono">{gateway?.url || 'http://localhost:3000'}</span>
                </div>

                <div className="flex items-center gap-2 pt-2">
                  <button
                    onClick={handleGatewayLogout}
                    className="flex-1 py-2.5 rounded-xl bg-[#20222a] hover:bg-rose-950/40 hover:text-rose-300 hover:border-rose-800/40 border border-[#2b2d38] text-xs font-semibold text-gray-300 transition cursor-pointer"
                  >
                    Sign Out
                  </button>
                  <button
                    onClick={() => setIsAuthModalOpen(false)}
                    className="flex-1 py-2.5 rounded-xl bg-gradient-to-r from-cyan-500 to-emerald-500 hover:from-cyan-400 hover:to-emerald-400 text-gray-950 text-xs font-bold transition cursor-pointer"
                  >
                    Done
                  </button>
                </div>
              </div>
            ) : (
              /* Sign In / Register View */
              <form onSubmit={handleGatewayLogin} className="flex flex-col gap-4">
                <p className="text-xs text-gray-400 leading-relaxed">
                  Enter your email to connect with your AH-Code account and sync usage tokens across devices.
                </p>

                <div className="flex flex-col gap-1.5">
                  <label className="text-[11px] font-medium text-gray-300">Email Address</label>
                  <input
                    type="email"
                    required
                    placeholder="you@example.com"
                    value={loginEmail}
                    onChange={(e) => setLoginEmail(e.target.value)}
                    className="bg-[#181a20] border border-[#2a2c36] focus:border-cyan-500 text-white text-xs rounded-xl px-3.5 py-2.5 focus:outline-none transition"
                  />
                </div>

                {loginError && (
                  <div className="text-xs text-rose-400 bg-rose-950/30 border border-rose-800/40 px-3 py-2 rounded-xl">
                    {loginError}
                  </div>
                )}

                <div className="flex items-center gap-2 pt-1">
                  <button
                    type="button"
                    onClick={() => setIsAuthModalOpen(false)}
                    className="flex-1 py-2.5 rounded-xl bg-transparent hover:bg-[#20222a] text-xs font-medium text-gray-400 hover:text-white transition cursor-pointer border border-[#282a34]"
                  >
                    Cancel
                  </button>
                  <button
                    type="submit"
                    disabled={loginLoading}
                    className="flex-1 py-2.5 rounded-xl bg-gradient-to-r from-cyan-500 to-emerald-500 hover:from-cyan-400 hover:to-emerald-400 text-gray-950 text-xs font-bold transition cursor-pointer disabled:opacity-50"
                  >
                    {loginLoading ? 'Connecting...' : 'Sign In'}
                  </button>
                </div>
              </form>
            )}
          </div>
        </div>
      )}

      {/* Lightbox Image Preview Modal (Antigravity Style) */}
      {previewImage && (
        <div
          onClick={() => setPreviewImage(null)}
          className="fixed inset-0 z-[100] bg-black/85 backdrop-blur-md flex items-center justify-center p-4 cursor-zoom-out select-none animate-in fade-in duration-150"
        >
          <div className="relative max-w-5xl max-h-[92vh] flex flex-col items-center">
            <button
              onClick={() => setPreviewImage(null)}
              className="absolute -top-11 right-0 p-1.5 text-gray-300 hover:text-white bg-white/10 hover:bg-white/20 rounded-full transition cursor-pointer flex items-center gap-1 px-3 text-xs"
            >
              <X size={14} />
              <span>Close</span>
            </button>
            <img
              src={previewImage}
              alt="Preview"
              className="max-h-[85vh] max-w-full rounded-xl object-contain border border-[#31343f] shadow-2xl"
              onClick={(e) => e.stopPropagation()}
            />
          </div>
        </div>
      )}
    </div>
  );
}

export default App;
