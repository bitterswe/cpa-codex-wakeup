"""Load a built plugin through its public C ABI and verify the store identity and page."""

import base64
import ctypes as c
import json
import os
import sys
import tempfile
from pathlib import Path


class Buffer(c.Structure):
    _fields_ = [("ptr", c.c_void_p), ("size", c.c_size_t)]


Call = c.CFUNCTYPE(c.c_int, c.c_char_p, c.c_void_p, c.c_size_t, c.POINTER(Buffer))
Release = c.CFUNCTYPE(None, c.c_void_p, c.c_size_t)
Shutdown = c.CFUNCTYPE(None)


class Host(c.Structure):
    _fields_ = [("version", c.c_uint32), ("context", c.c_void_p),
                ("call", c.c_void_p), ("release", c.c_void_p)]


class Plugin(c.Structure):
    _fields_ = [("version", c.c_uint32), ("call", Call),
                ("release", Release), ("shutdown", Shutdown)]


def main():
    sandbox = tempfile.TemporaryDirectory()
    os.environ["CPA_CODEX_WAKEUP_DIR"] = sandbox.name
    library = c.CDLL(str(Path(sys.argv[1]).resolve()))
    init = library.cliproxy_plugin_init
    init.argtypes = [c.POINTER(Host), c.POINTER(Plugin)]
    init.restype = c.c_int
    plugin = Plugin()
    host_method = c.CFUNCTYPE(c.c_int, c.c_void_p, c.c_char_p, c.c_void_p, c.c_size_t, c.POINTER(Buffer))
    allocations = {}

    @host_method
    def host_call(context, method, data, length, out):
        if method != b"host.auth.list":
            return 1
        raw = json.dumps({"ok": True, "result": {"files": [
            {"provider": "codex", "auth_index": "fixture", "email": "demo@example.invalid"}
        ]}}).encode()
        buf = c.create_string_buffer(raw)
        addr = c.addressof(buf)
        allocations[addr] = buf
        out[0] = Buffer(addr, len(raw))
        return 0

    @Release
    def free_host(ptr, length):
        del allocations[ptr]

    host = Host(1, None, c.cast(host_call, c.c_void_p), c.cast(free_host, c.c_void_p))
    assert init(c.byref(host), c.byref(plugin)) == 0 and plugin.version == 1

    def invoke(method, payload=None):
        raw = json.dumps(payload).encode()
        output = Buffer()
        assert plugin.call(method.encode(), raw, len(raw), c.byref(output)) == 0
        try:
            response = json.loads(c.string_at(output.ptr, output.size))
            assert response["ok"], response
            return response["result"]
        finally:
            plugin.release(output.ptr, output.size)

    registry = Path(__file__).resolve().parents[1] / "registry.json"
    entry, = json.loads(registry.read_text(encoding="utf-8"))["plugins"]
    registration = invoke("plugin.register")
    assert registration["capabilities"] == {"management_api": True}
    assert invoke("plugin.reconfigure") == registration
    metadata = registration["metadata"]
    assert metadata["GitHubRepository"] == entry["repository"]
    resource, = invoke("management.register")["Resources"]
    assert resource["Menu"] == entry["name"] == metadata["Name"]
    path = f"/v0/resource/plugins/{entry['id']}{resource['Path']}"
    page = invoke("management.handle", {"Method": "GET", "Path": path})
    assert page["StatusCode"] == 200 and "text/html" in page["Headers"]["Content-Type"][0]
    html = base64.b64decode(page["Body"]).decode()
    assert entry["name"] in html and f'v{metadata["Version"]}' in html
    assert invoke("management.handle", {"Method": "GET", "Path": "/"})["StatusCode"] == 404
    assert invoke("management.handle", {"Method": "POST", "Path": path})["StatusCode"] == 404
    def admin(action=None, **data):
        payload = {"Method": "GET", "Path": "/v0/management/plugins/cpa-codex-wakeup/tasks"}
        if action:
            payload.update(Method="POST", Body=base64.b64encode(json.dumps({"Action": action, **data}).encode()).decode())
        result = invoke("management.handle", payload)
        assert result["StatusCode"] == 200, result
        return json.loads(base64.b64decode(result["Body"]))

    task = dict(Name="ABI fixture", Model="fixture", Kind="weekly", Days=[1, 5], Clock="10:00",
                Zone="Asia/Shanghai", Mode="confirm", Timeout=5, IDs=["fixture"], Enabled=True)
    assert admin("accounts")[0]["ID"] == "fixture" and not allocations
    assert len(admin("preview", Task=task)) == 5
    admin("save", Task=task)
    task_id = admin()["state"]["Tasks"][0]["ID"]
    admin("run", ID=task_id)
    pending = admin()["state"]["Runs"][-1]
    assert pending["Status"] == "pending"
    admin("skip", ID=pending["ID"])
    assert admin()["state"]["Runs"][-1]["Status"] == "skipped"
    invoke("plugin.quiesce")
    invoke("plugin.register")
    assert admin()["state"]["Tasks"][0]["ID"] == task_id
    admin("delete", ID=task_id)
    invoke("plugin.shutdown")
    plugin.shutdown()
    sandbox.cleanup()
    print(metadata["Version"])


if __name__ == "__main__":
    main()
