FROM python:3.10-alpine

WORKDIR /app

ENV PYTHONUNBUFFERED=1 \
    IMAGE_TEST_SOURCE=remote

COPY app.py /app/app.py

CMD ["python", "/app/app.py"]
