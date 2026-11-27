from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse

from app.api.v1.analysis import router as analysis_router
from app.api.v1.datasets import router as datasets_router
from app.api.v1.explorer import router as explorer_router
from app.api.v1.health import router as health_router
from app.data_engine.errors import DatasetError

app = FastAPI(title="InsightFlow Engine", version="0.1.0")
app.include_router(health_router, prefix="/v1")
app.include_router(datasets_router, prefix="/v1")
app.include_router(explorer_router, prefix="/v1")
app.include_router(analysis_router, prefix="/v1")


@app.exception_handler(DatasetError)
async def dataset_error_handler(request: Request, exc: DatasetError) -> JSONResponse:
    """Emit the shared error envelope: {error: {code, message, retryable, details}}."""
    return JSONResponse(
        status_code=exc.http_status,
        content={
            "error": {
                "code": exc.code,
                "message": exc.message,
                "retryable": exc.retryable,
                "details": exc.details,
            }
        },
    )


if __name__ == "__main__":
    import uvicorn

    from app.config import settings

    uvicorn.run("app.main:app", host=settings.host, port=settings.port, reload=True)
