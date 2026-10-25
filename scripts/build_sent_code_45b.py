import os
import sys
import subprocess
import shutil
import time

BASE_DIR = r"C:\Drive-D\My-Program\Sent-Code"
MODELS_DIR = os.path.join(BASE_DIR, "models")
CONFIG_YAML = os.path.join(MODELS_DIR, "merge_4.5b.yaml")
WORKSPACE_DIR = os.path.join(MODELS_DIR, "workspace")
OUTPUT_HF_DIR = os.path.join(WORKSPACE_DIR, "sent-code-4.5b-hf")
OUTPUT_GGUF = os.path.join(MODELS_DIR, "sent-code-4.5b-q4_k_m.gguf")
LLAMA_CPP_DIR = os.path.join(MODELS_DIR, "llama.cpp")

def run(cmd, desc):
    print(f"\n========================================================")
    print(f"🚀 {desc}")
    print(f"Command: {cmd}")
    print(f"========================================================")
    start = time.time()
    res = subprocess.run(cmd, shell=True)
    if res.returncode != 0:
        print(f"❌ Error during: {desc} (Exit code: {res.returncode})")
        sys.exit(res.returncode)
    print(f"✅ Finished in {time.time() - start:.1f}s")

def main():
    os.makedirs(WORKSPACE_DIR, exist_ok=True)

    # Step 1: Run mergekit-yaml
    if not os.path.exists(OUTPUT_HF_DIR) or not os.listdir(OUTPUT_HF_DIR):
        print(f"Step 1: Running mergekit-yaml on {CONFIG_YAML}...")
        merge_cmd = (
            f'mergekit-yaml '
            f'"{CONFIG_YAML}" "{OUTPUT_HF_DIR}" '
            f'--clone-tensors --allow-crimes --safe-serialization --out-shard-size 2B --copy-tokenizer'
        )
        run(merge_cmd, "Merging Qwen Layers into Sent-Code 4.5B")
    else:
        print(f"Merged HF weights already exist at {OUTPUT_HF_DIR}")

    # Step 2: Convert to GGUF using llama.cpp
    convert_script = os.path.join(LLAMA_CPP_DIR, "convert_hf_to_gguf.py")
    if os.path.exists(convert_script):
        convert_cmd = (
            f'python "{convert_script}" "{OUTPUT_HF_DIR}" '
            f'--outtype q8_0 --outfile "{OUTPUT_GGUF}"'
        )
        run(convert_cmd, "Converting Merged Model to GGUF")
    else:
        print(f"❌ convert_hf_to_gguf.py not found at {convert_script}")
        sys.exit(1)

    # Step 3: Register with Sent-Code Ollama Sidecar
    localappdata = os.environ.get("LOCALAPPDATA", "")
    ollama_bin = os.path.join(localappdata, "Sent-Code", "engine", "ollama", "ollama.exe")
    models_dir = os.path.join(localappdata, "Sent-Code", "engine", "models")

    if os.path.exists(ollama_bin):
        modelfile_path = os.path.join(MODELS_DIR, "Modelfile")
        with open(modelfile_path, "w", encoding="utf-8") as f:
            f.write(f'''FROM {OUTPUT_GGUF}
TEMPLATE """{{{{ if .System }}}}<|im_start|>system
{{{{ .System }}}}<|im_end|>
{{{{ end }}}}{{{{ if .Prompt }}}}<|im_start|>user
{{{{ .Prompt }}}}<|im_end|>
{{{{ end }}}}<|im_start|>assistant
{{{{ .Response }}}}<|im_end|>"""
PARAMETER stop "<|im_start|>"
PARAMETER stop "<|im_end|>"
PARAMETER temperature 0.2
''')
        print(f"Created Modelfile at {modelfile_path}")
        
        # Set OLLAMA_MODELS env so it creates inside Sent-Code private directory
        os.environ["OLLAMA_MODELS"] = models_dir
        create_cmd = f'"{ollama_bin}" create sent-code:4.5b -f "{modelfile_path}"'
        run(create_cmd, "Importing sent-code:4.5b into Sent-Code Engine")

    print("\n🎉 ALL DONE! Sent-Code 4.5B is fully built and installed in Sent-Code IDE!")
    print(f"Standalone GGUF file: {OUTPUT_GGUF}")

if __name__ == "__main__":
    main()
