"""R22 checks mixed into the guarded, isolated HTTP acceptance suite."""
import concurrent.futures
import urllib.parse
import uuid


class AdminAccountsChecks:
    def r22_client(self):
        return type(self.clients["admin"])(self.args.admin_url, self.args.admin_origin)

    def r22_login(self, client, login, password, status=200):
        return self.expect("R22 administrator password login", client.request("POST", "/api/v1/auth/login",
                           {"method": "password", "phone": login, "password": password}), status)

    def r22_find(self, login):
        response = self.call("admin", "GET", "/api/v1/admin/administrators?" + urllib.parse.urlencode({"q": login, "limit": 20}))
        rows = [row for row in response.data if row["login"] == login]
        self.check("R22 exact login identifies one administrator", len(rows) == 1)
        return rows[0]

    def r22_create(self, name, avatar=""):
        login = "r22_" + uuid.uuid4().hex[:16]
        password = "R22-" + uuid.uuid4().hex
        body = {"login": login, "nickname": name, "password": password, "avatar_id": avatar, "request_id": str(uuid.uuid4())}
        result = self.call("admin", "POST", "/api/v1/admin/administrators", body, status=(200, 201)).data
        self.check("R22 created administrator has bounded public fields", result.get("login") == login and result.get("enabled") is True
                   and result.get("version", 0) > 0 and not ({"password", "password_hash", "request_hash"} & result.keys()))
        return result, body

    def r22_patch(self, account, **values):
        return self.call("admin", "PATCH", f"/api/v1/admin/administrators/{account['id']}",
                         dict(expected_version=account["version"], **values)).data

    def r22_functional(self):
        member = self.credentials["member"]
        result = self.call("admin", "GET", "/api/v1/admin/users?" + urllib.parse.urlencode({"role": "member", "q": member["phone"]}))
        self.check("R22 member list returns the full phone only to admins", len(result.data) == 1
                   and result.data[0].get("phone") == member["phone"] and result.data[0].get("role") == "member")
        self.check("R22 member listing is uncached", "no-store" in result.headers.get("Cache-Control", ""))
        member_rows = self.call("admin", "GET", "/api/v1/admin/users?role=member&limit=50").data
        self.check("R22 member list excludes administrators", all(row.get("role") == "member" for row in member_rows))
        identity = self.call("member", "GET", "/api/v1/me").data
        self.check("R22 public identity still masks member phones", identity.get("phone_masked") != member["phone"] and "phone" not in identity)

        media_id = self.upload("admin", purpose="avatar")
        avatar = self.call("admin", "POST", "/api/v1/admin/avatar-library", {"name": "R22验收头像", "media_id": media_id,
                           "sort_order": 900, "enabled": True}).data
        account, body = self.r22_create("R22验收管理员", media_id)
        path = f"/api/v1/admin/administrators/{account['id']}"
        self.fixtures["r22_admin_id"] = account["id"]
        replay = self.call("admin", "POST", "/api/v1/admin/administrators", body, status=(200, 201)).data
        self.check("R22 creation replay returns the same account", replay == account)
        self.call("admin", "POST", "/api/v1/admin/administrators", dict(body, nickname="changed"), status=409)
        self.call("admin", "POST", "/api/v1/admin/administrators", dict(body, request_id=str(uuid.uuid4())), status=409)
        client = self.r22_client()
        self.r22_login(client, body["login"], body["password"])
        listed = self.expect("R22 new admin has the same administrator management access", client.request("GET", "/api/v1/admin/administrators")).data
        self.check("R22 administrators have no permission tiers", isinstance(listed, list) and any(row["id"] == account["id"] for row in listed))
        self.expect("R22 new admin can read member phones", client.request("GET", "/api/v1/admin/users?role=member"))
        peer = self.expect("R22 new admin can create another equal administrator", client.request("POST", "/api/v1/admin/administrators",
                           {"login": "r22_" + uuid.uuid4().hex[:16], "nickname": "R22同权验证", "password": "R22-" + uuid.uuid4().hex,
                            "request_id": str(uuid.uuid4())}), (200, 201)).data
        self.expect("R22 new admin can manage another administrator", client.request("PATCH", f"/api/v1/admin/administrators/{peer['id']}",
                    {"expected_version": peer["version"], "enabled": False}))
        self.expect("R22 self-disable remains prohibited", client.request("PATCH", path, {"expected_version": account["version"], "enabled": False}), 422)
        self.call("admin", "PATCH", f"/api/v1/admin/users/{account['id']}", {"enabled": False}, status=409)
        self.expect("R22 old profile endpoint cannot bypass administrator versions", client.request("PATCH", "/api/v1/admin/chat/profile", {"avatar_id": ""}), 422)

        updated = self.expect("R22 admin name changes immediately", client.request("PATCH", path,
                              {"expected_version": account["version"], "nickname": "R22即时名称"})).data
        self.check("R22 name update increments version", updated["nickname"] == "R22即时名称" and updated["version"] == account["version"] + 1)
        self.call("admin", "PATCH", path, {"expected_version": account["version"], "nickname": "stale"}, status=409)
        self.check("R22 refreshed session sees the new name", self.expect("R22 refreshed admin identity", client.request("GET", "/api/v1/me")).data["nickname"] == "R22即时名称")
        self.call("admin", "PATCH", path, {"expected_version": updated["version"], "avatar_id": "not-an-enabled-avatar"}, status=422)
        updated = self.r22_patch(updated, avatar_id="")
        self.check("R22 avatar can be reset to default", updated.get("avatar_id") in ("", None))
        updated = self.r22_patch(updated, avatar_id=media_id)
        self.check("R22 enabled avatar library item can be selected", updated.get("avatar_id") == media_id and bool(updated.get("avatar_url")))
        self.call("admin", "PATCH", f"/api/v1/admin/avatar-library/{avatar['id']}",
                  {"name": avatar["name"], "sort_order": avatar["sort_order"], "enabled": False, "version": avatar["version"]})
        updated = self.r22_patch(updated, nickname="R22保留原头像", avatar_id=media_id)
        self.check("R22 disabled existing avatar does not block name edits", updated.get("avatar_id") == media_id)
        updated = self.r22_patch(updated, avatar_id="")
        self.call("admin", "PATCH", path, {"expected_version": updated["version"], "avatar_id": media_id}, status=422)

        new_password = "R22-" + uuid.uuid4().hex
        reset = {"expected_version": updated["version"], "password": new_password, "request_id": str(uuid.uuid4())}
        response = self.call("admin", "POST", path + "/password", reset).data
        self.check("R22 reset response contains no credentials", not ({"password", "password_hash", "request_hash"} & response.keys()))
        self.check("R22 password reset replay is stable", self.call("admin", "POST", path + "/password", reset).data == response)
        self.call("admin", "POST", path + "/password", dict(reset, password="R22-" + uuid.uuid4().hex), status=409)
        self.expect("R22 password reset revokes the target session", client.request("GET", "/api/v1/me"), 401)
        self.r22_login(client, body["login"], body["password"], 401)
        self.r22_login(client, body["login"], new_password)
        updated = self.r22_find(body["login"])
        updated = self.r22_patch(updated, enabled=False)
        self.expect("R22 disable revokes the target session", client.request("GET", "/api/v1/me"), 401)
        self.r22_login(client, body["login"], new_password, 401)
        updated = self.r22_patch(updated, enabled=True)
        self.expect("R22 reenable does not restore the old session", client.request("GET", "/api/v1/me"), 401)
        self.r22_login(client, body["login"], new_password)
        self.r22_patch(updated, enabled=False)

    def r22_concurrency(self):
        account, body = self.r22_create("R22并发验收")
        path = f"/api/v1/admin/administrators/{account['id']}"
        def update(index):
            return self.clients["admin"].request("PATCH", path, {"expected_version": account["version"], "nickname": f"R22并发{index}"})
        with concurrent.futures.ThreadPoolExecutor(max_workers=8) as executor:
            replies = list(executor.map(update, range(16)))
        statuses = [reply.status for reply in replies]
        self.check("R22 same version admits one administrator update", statuses.count(200) == 1 and statuses.count(409) == 15,
                   successes=statuses.count(200), conflicts=statuses.count(409))
        updated = self.r22_find(body["login"])
        self.check("R22 concurrent updates increment version once", updated["version"] == account["version"] + 1)
        self.r22_patch(updated, enabled=False)

    def r22_security(self):
        admin = self.clients["admin"]
        path = "/api/v1/admin/administrators"
        self.call("member", "GET", path, status=404)
        self.expect("R22 anonymous administrator list denied", admin.request("GET", path, cookie=""), 401)
        self.expect("R22 anonymous full phone listing denied", admin.request("GET", "/api/v1/admin/users", cookie=""), 401)
        self.expect("R22 renamed member cookie cannot read full phones", admin.request("GET", "/api/v1/admin/users",
                    cookie="qingye_admin_session=" + self.clients["member"].cookies["qingye_session"]), 401)
        for origin in ("", "null", self.args.public_origin):
            self.expect("R22 create requires administrator Origin", admin.request("POST", path, {}, origin=origin), 403)
        sample = {"login": "r22_" + uuid.uuid4().hex[:16], "nickname": "R22非法请求", "password": "R22-" + uuid.uuid4().hex, "request_id": str(uuid.uuid4())}
        for values in ({"role": "superadmin"}, {"login": "x"}, {"nickname": ""}, {"password": "short"},
                       {"password": "a" * 73}, {"avatar_id": "unavailable"}, {"request_id": ""}):
            self.call("admin", "POST", path, dict(sample, **values), status=422)
        self.call("admin", "PATCH", f"{path}/{self.fixtures['member_user_id']}", {"expected_version": 1, "nickname": "cannot promote"}, status=404)
        self.call("admin", "POST", f"{path}/{self.fixtures['member_user_id']}/password", {"expected_version": 1,
                  "password": "R22-" + uuid.uuid4().hex, "request_id": str(uuid.uuid4())}, status=404)
        self.call("admin", "GET", "/api/v1/admin/users?role=superadmin", status=422)
