from fastapi import FastAPI

from app.api.v1.health import router as health_router

app = FastAPI(title="InsightFlow Engine", version="0.1.0")
app.include_router(health_router, prefix="/v1")


if __name__ == "__main__":
    import uvicorn

    from app.config import settings

    uvicorn.run("app.main:app", host=settings.host, port=settings.port, reload=True)
