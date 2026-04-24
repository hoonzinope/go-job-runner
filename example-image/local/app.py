import json
import os
import socket
import time
from datetime import datetime, timezone


def main() -> None:
    time.sleep(5)

    payload = {
        "source": os.getenv("IMAGE_TEST_SOURCE", "local"),
        "hostname": socket.gethostname(),
        "timestamp": datetime.now(timezone.utc).isoformat(),
        "job_params": os.getenv("JOB_PARAMS", ""),
    }
    print(
        f"hello world image test {json.dumps(payload, ensure_ascii=False, sort_keys=True)}",
        flush=True,
    )


if __name__ == "__main__":
    main()
