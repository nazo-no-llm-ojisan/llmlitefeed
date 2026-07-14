import os
import sys
import time
import json
from pydantic import BaseModel, Field
from openai import OpenAI
from google import genai

# .env ファイルがあればロードする
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

# スキーマ定義
class FeedItem(BaseModel):
    title: str = Field(description="記事のタイトル")
    summary: str = Field(description="日本語による簡潔な3行程度の要約")
    key_points: list[str] = Field(description="この記事の重要なポイント（最大3つ）の箇造書きリスト")
    category: str = Field(description="記事のカテゴリ（「技術」「ビジネス」「モデル発表」「ツール」など）")
    score: int = Field(description="記事の重要度（1から5の数値、5が最も重要）")
    url: str = Field(description="元記事のURL")
    published_at: str = Field(description="記事の公開日、または収集日（YYYY-MM-DD形式）")

# テストデータ
TEST_TITLE = "Apple's trade secrets lawsuit against OpenAI"
TEST_URL = "https://techcrunch.com/2026/07/apples-trade-secrets-lawsuit-against-openai"
TEST_TEXT = """
Apple has filed a lawsuit against OpenAI, alleging that the ChatGPT maker systematically poached employees and misappropriated trade secrets. 
The lawsuit, filed in the Northern District of California, details several incidents where former Apple engineers allegedly downloaded confidential documents related to Apple's secret AI models before leaving for OpenAI. 
Apple is seeking damages and an injunction to prevent OpenAI from using any intellectual property originating from Apple. 
OpenAI has denied the allegations, stating that they respect intellectual property and recruit talent through standard competitive practices. 
The case highlights the intensifying talent war and intellectual property disputes in the artificial intelligence sector, as tech giants struggle to dominate the generative AI market.
"""

def run_gemini_test():
    print("\n--- Testing Gemini (gemini-2.5-flash) ---")
    api_key = os.environ.get("GEMINI_API_KEY")
    if not api_key:
        print("Skipped: GEMINI_API_KEY is not set.")
        return None
    
    start_time = time.time()
    try:
        client = genai.Client()
        prompt = f"記事タイトル: {TEST_TITLE}\nURL: {TEST_URL}\n本文: {TEST_TEXT}"
        response = client.models.generate_content(
            model='gemini-2.5-flash',
            contents=prompt,
            config={
                'response_mime_type': 'application/json',
                'response_schema': FeedItem,
            }
        )
        duration = time.time() - start_time
        item = response.parsed
        print(f"Status: SUCCESS")
        print(f"Time Taken: {duration:.2f}s")
        print(f"Output:\n{json.dumps(item.model_dump(), ensure_ascii=False, indent=2)}")
        return {"model": "gemini-2.5-flash", "success": True, "time": duration}
    except Exception as e:
        print(f"Status: FAILED. Error: {e}")
        return {"model": "gemini-2.5-flash", "success": False}

def run_ollama_test():
    print("\n--- Testing Ollama (qwen2.5:1.5b) ---")
    start_time = time.time()
    try:
        client = OpenAI(base_url="http://localhost:11434/v1", api_key="ollama")
        prompt = f"記事タイトル: {TEST_TITLE}\nURL: {TEST_URL}\n本文: {TEST_TEXT}"
        
        response = client.chat.completions.create(
            model="qwen2.5:1.5b",
            temperature=0,
            messages=[
                {"role": "system", "content": "以下の記事を要約し、指定されたスキーマに従ってJSONを出力してください。"},
                {"role": "user", "content": prompt}
            ],
            response_format={
                "type": "json_schema",
                "json_schema": {"name": "feed_item", "schema": FeedItem.model_json_schema()}
            }
        )
        duration = time.time() - start_time
        result_str = response.choices[0].message.content
        data = json.loads(result_str)
        item = FeedItem.model_validate(data)
        print(f"Status: SUCCESS")
        print(f"Time Taken: {duration:.2f}s")
        print(f"Output:\n{json.dumps(item.model_dump(), ensure_ascii=False, indent=2)}")
        return {"model": "qwen2.5:1.5b (Ollama)", "success": True, "time": duration}
    except Exception as e:
        print(f"Status: FAILED. Error: {e}")
        return {"model": "qwen2.5:1.5b (Ollama)", "success": False}

def run_agnes_test():
    print("\n--- Testing Agnes AI (agnes-2.0-flash) ---")
    api_key = os.environ.get("AGNES_API_KEY")
    api_url = os.environ.get("AGNES_API_URL") or "https://apihub.agnes-ai.com/v1"
    model_name = os.environ.get("AGNES_API_MODEL") or "agnes-2.0-flash"
    
    if not api_key:
        print("Skipped: AGNES_API_KEY is not set.")
        return None
        
    start_time = time.time()
    try:
        client = OpenAI(base_url=api_url, api_key=api_key)
        prompt = f"記事タイトル: {TEST_TITLE}\nURL: {TEST_URL}\n本文: {TEST_TEXT}"
        
        response = client.chat.completions.create(
            model=model_name,
            temperature=0,
            messages=[
                {"role": "system", "content": "以下の記事を要約し、指定されたスキーマに従ってJSONを出力してください。"},
                {"role": "user", "content": prompt}
            ],
            response_format={
                "type": "json_schema",
                "json_schema": {"name": "feed_item", "schema": FeedItem.model_json_schema()}
            }
        )
        duration = time.time() - start_time
        result_str = response.choices[0].message.content
        data = json.loads(result_str)
        item = FeedItem.model_validate(data)
        print(f"Status: SUCCESS")
        print(f"Time Taken: {duration:.2f}s")
        print(f"Output:\n{json.dumps(item.model_dump(), ensure_ascii=False, indent=2)}")
        return {"model": model_name, "success": True, "time": duration}
    except Exception as e:
        print(f"Status: FAILED. Error: {e}")
        return {"model": model_name, "success": False}

def main():
    print("=========================================")
    print("Starting LLM Benchmark Test")
    print("=========================================")
    
    results = []
    
    # 1. Gemini
    res_gemini = run_gemini_test()
    if res_gemini:
        results.append(res_gemini)
        
    # 2. Ollama
    res_ollama = run_ollama_test()
    if res_ollama:
        results.append(res_ollama)
        
    # 3. Agnes AI
    res_agnes = run_agnes_test()
    if res_agnes:
        results.append(res_agnes)
        
    print("\n=========================================")
    print("Benchmark Summary")
    print("=========================================")
    for r in results:
        status_str = "SUCCESS" if r["success"] else "FAILED"
        time_str = f"{r['time']:.2f}s" if r["success"] else "N/A"
        print(f"- {r['model']}: {status_str} (Time: {time_str})")
    print("=========================================")

if __name__ == "__main__":
    main()
