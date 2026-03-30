import sys
try:
    from fastapi import FastAPI, Body
    from sentence_transformers import SentenceTransformer
    import uvicorn
    import torch
except ImportError:
    print("Missing dependencies: fastapi, sentence-transformers, uvicorn")
    sys.exit(1)

app = FastAPI()

# Load model once on startup
# Using 'all-MiniLM-L6-v2' for 384 dimensions - fast and accurate enough for search
model_name = 'all-MiniLM-L6-v2'
print(f"Loading embedding model: {model_name}...")
model = SentenceTransformer(model_name)

@app.get("/health")
def health():
    return {"status": "ok", "model": model_name}

@app.post("/embed")
def embed(body: dict = Body(...)):
    text = body.get("text", "")
    if not text:
        return {"embedding": []}
    
    # Generate embedding
    embedding = model.encode(text)
    
    # Convert to list for JSON serialization
    return {"embedding": embedding.tolist()}

if __name__ == "__main__":
    uvicorn.run(app, host="0.0.0.0", port=5000)
