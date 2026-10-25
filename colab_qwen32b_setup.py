# ==============================================================================
# One-Click Setup: Ollama + Qwen 2.5 Coder 32B + Cloudflare Tunnel for AH-Code
# ==============================================================================
# วิธีใช้งานบน Google Colab:
# 1. ไปที่ https://colab.research.google.com
# 2. เมนู Runtime -> Change runtime type -> Hardware accelerator -> เลือก A100 (หรือ T4)
# 3. วางโค้ดนี้ทั้งหมดลงในช่อง แล้วกดปุ่ม Run (Play)
# 4. เมื่อรันเสร็จ จะมี URL https://xxxx.trycloudflare.com แสดงขึ้นมา
# 5. นำ URL นั้นไปวางใน Settings ของโปรแกรม AH-Code แล้วเริ่มใช้งาน Qwen 32B ได้เลย!
# ==============================================================================

import os
import subprocess
import time
import threading
import urllib.request
import re

print("🚀 เริ่มต้นติดตั้ง Ollama และ Cloudflare Tunnel...")

# 1. ติดตั้ง zstd และ Ollama
subprocess.run("apt-get update -qq && apt-get install -y -qq zstd", shell=True, check=True)
if not os.path.exists("/usr/local/bin/ollama"):
    subprocess.run("curl -fsSL https://ollama.com/install.sh | sh", shell=True, check=True)

# 2. ติดตั้ง Cloudflared
if not os.path.exists("/usr/local/bin/cloudflared"):
    subprocess.run("curl -s -Lo /usr/local/bin/cloudflared https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64", shell=True, check=True)
    subprocess.run("chmod +x /usr/local/bin/cloudflared", shell=True, check=True)

print("✅ ติดตั้งพื้นฐานสำเร็จ กำลังเปิดเซิร์ฟเวอร์...")

# 3. สั่ง Ollama Serve ทำงานเบื้องหลัง
env = os.environ.copy()
env["OLLAMA_HOST"] = "0.0.0.0:11434"
env["OLLAMA_ORIGINS"] = "*"
subprocess.Popen(["ollama", "serve"], env=env)

# รอให้ Ollama พร้อม
for _ in range(15):
    try:
        with urllib.request.urlopen("http://localhost:11434/") as resp:
            if resp.status == 200: break
    except: time.sleep(1)

# 4. ดาวน์โหลด Qwen 2.5 Coder 32B (A100 โหลดเร็วมาก)
print("⬇️ กำลังโหลด Qwen 2.5 Coder 32B เข้าการ์ดจอ (รอประมาณ 2-3 นาที)...")
subprocess.run(["ollama", "pull", "qwen2.5-coder:32b"], check=True)
print("✅ โหลดโมเดล 32B เรียบร้อยแล้ว!")

# 5. เปิด Cloudflare Tunnel
print("\n🌐 กำลังสร้าง URL ปลอดภัยสำหรับเชื่อมต่อกับ AH-Code...")
proc = subprocess.Popen(["cloudflared", "tunnel", "--url", "http://localhost:11434"],
                        stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)

for line in iter(proc.stdout.readline, ''):
    m = re.search(r'https://[a-zA-Z0-9-]+\.trycloudflare\.com', line)
    if m:
        url = m.group(0)
        print("\n" + "="*70)
        print("🎉 สำเร็จ! คัดลอกลิงก์นี้ไปวางในโปรแกรม AH-Code ในช่อง Remote Cloud GPU:")
        print(f"👉 {url}")
        print("="*70 + "\n")
        break
