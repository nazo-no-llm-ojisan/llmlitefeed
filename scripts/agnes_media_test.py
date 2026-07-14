import os
import sys
import time
import requests
from openai import OpenAI

# .env ファイルをロードする
def load_env():
    if os.path.exists(".env"):
        with open(".env", "r", encoding="utf-8") as f:
            for line in f:
                if "=" in line and not line.strip().startswith("#"):
                    k, v = line.strip().split("=", 1)
                    os.environ[k.strip()] = v.strip().strip("'").strip('"')

load_env()

# WindowsでのUnicode文字エンコードエラーを防止
if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(encoding="utf-8")

def download_file(url, local_filename):
    print(f"Downloading {url} to {local_filename}...")
    try:
        with requests.get(url, stream=True, timeout=30) as r:
            r.raise_for_status()
            with open(local_filename, 'wb') as f:
                for chunk in r.iter_content(chunk_size=8192):
                    f.write(chunk)
        print(f" -> Saved to: {os.path.abspath(local_filename)}")
        return True
    except Exception as e:
        print(f" -> Failed to download: {e}")
        return False

def run_image_generation(client):
    print("\n=========================================")
    print("1. Testing Image Generation (agnes-image-2.1-flash)")
    print("=========================================")
    prompt = "A beautiful cyberpunk city street at night, neon lights, rainy weather, ultra-detailed 4k"
    print(f"Prompt: {prompt}")
    
    try:
        start_time = time.time()
        response = client.images.generate(
            model="agnes-image-2.1-flash",
            prompt=prompt,
            size="1024x1024",
            n=1
        )
        duration = time.time() - start_time
        img_url = response.data[0].url
        print(f"Success! Time taken: {duration:.2f}s")
        print(f"Image URL: {img_url}")
        
        # ローカルに保存
        download_file(img_url, "output_image.png")
    except Exception as e:
        print(f"Failed to generate image: {e}")

def run_video_generation(api_key, base_url):
    print("\n=========================================")
    print("2. Testing Video Generation (agnes-video-v2.0)")
    print("=========================================")
    prompt = "A cinematic shot of a futuristic spaceship landing in a cyberpunk city harbor, neon reflections on water"
    print(f"Prompt: {prompt}")
    
    headers = {
        "Authorization": f"Bearer {api_key}",
        "Content-Type": "application/json"
    }
    
    # モデル名を agnes-video-v2.0 で試す（ダメなら agnes-video-2.0）
    payload = {
        "model": "agnes-video-v2.0",
        "prompt": prompt
    }
    
    try:
        start_time = time.time()
        print("Sending video generation task request...")
        resp = requests.post(f"{base_url}/videos", headers=headers, json=payload, timeout=30)
        
        # エラーならモデル名を変えてリトライ
        if resp.status_code != 200:
            print(f"Failed with agnes-video-v2.0 (Code {resp.status_code}). Retrying with agnes-video-2.0...")
            payload["model"] = "agnes-video-2.0"
            resp = requests.post(f"{base_url}/videos", headers=headers, json=payload, timeout=30)
            
        resp.raise_for_status()
        data = resp.json()
        task_id = data.get("id")
        print(f"Task created successfully. Task ID: {task_id}")
        
        # ポーリング
        poll_interval = 10
        max_polls = 30  # 最大5分間
        
        print("Polling for video generation status...")
        for i in range(max_polls):
            status_resp = requests.get(f"{base_url}/videos/{task_id}", headers=headers, timeout=30)
            status_resp.raise_for_status()
            status_data = status_resp.json()
            
            status = status_data.get("status")
            print(f" -> Status check {i+1}: {status}")
            
            if status == "completed":
                video_url = status_data.get("url") or status_data.get("metadata", {}).get("url")
                duration = time.time() - start_time
                print(f"Success! Total time taken: {duration:.2f}s")
                print(f"Video URL: {video_url}")
                download_file(video_url, "output_video.mp4")
                return
            elif status == "failed":
                print("Video generation failed on server.")
                return
            
            time.sleep(poll_interval)
            
        print("Polling timed out.")
    except Exception as e:
        print(f"Failed to generate video: {e}")

def main():
    api_key = os.environ.get("AGNES_API_KEY")
    base_url = os.environ.get("AGNES_API_URL") or "https://apihub.agnes-ai.com/v1"
    
    if not api_key:
        print("Error: AGNES_API_KEY is not set in environment or .env file.")
        sys.exit(1)
        
    client = OpenAI(api_key=api_key, base_url=base_url)
    
    # 1. 画像生成
    run_image_generation(client)
    
    # 2. 動画生成
    run_video_generation(api_key, base_url)

if __name__ == "__main__":
    main()
