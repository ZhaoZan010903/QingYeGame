"""R23 recommendation and category checks for the isolated HTTP suite."""
import concurrent.futures
import urllib.parse
import uuid


class RecommendationBoostChecks:
    def r23_boost(self, product, value, status=200):
        return self.call("admin", "PATCH", f"/api/v1/admin/products/{product['id']}/recommendation",
                         {"boost": value, "expected_version": product["recommendation_version"]}, status=status).data

    def r23_order(self, query, sort):
        params = {"q": query, "sort": sort, "limit": 20}
        if sort.startswith("price"):
            params["price_unit"] = "account"
        rows = self.call("anonymous", "GET", "/api/v1/products?" + urllib.parse.urlencode(params)).data
        return [row["id"] for row in rows]

    def r23_functional(self):
        prefix = self.state["run_id"] + " r23"
        body = self.product_input(prefix + " automatic sale category")
        body.pop("category_id")
        automatic = self.call("admin", "POST", "/api/v1/admin/products", body, status=(200, 201)).data
        self.fixtures["r23_automatic_product_id"] = automatic["id"]
        categories = self.call("admin", "GET", "/api/v1/admin/categories").data
        category = next((row for row in categories if row["id"] == automatic["category_id"]), {})
        self.check("R23 sale chooses an enabled compatible category on the server", category.get("enabled")
                   and category.get("trade_type") in ("sale", "all")
                   and (not category.get("price_units") or "account" in category["price_units"]))
        replay = self.call("admin", "POST", "/api/v1/admin/products", body, status=(200, 201)).data
        self.check("R23 automatic category creation keeps request idempotency", replay["id"] == automatic["id"])
        self.call("admin", "POST", "/api/v1/admin/products", dict(body, request_id=str(uuid.uuid4()),
                  category_id="r23-unavailable-category"), status=422)
        self.call("member", "POST", "/api/v1/me/products", dict(body, request_id=str(uuid.uuid4())), status=422)

        before = self.admin_product(self.fixtures["platform_product_id"])
        self.check("R23 existing product starts with zero boost and a separate version",
                   before["recommendation_boost"] == 0 and before["recommendation_version"] >= 1)
        mutation = self.r23_boost(before, 45)
        self.check("R23 recommendation edit returns its own version", mutation["id"] == before["id"]
                   and mutation["recommendation_boost"] == 45
                   and mutation["recommendation_version"] == before["recommendation_version"] + 1)
        after = self.admin_product(before["id"])
        unchanged = ("version", "approved_version", "status", "published_at", "updated_at", "title", "category_id")
        self.check("R23 recommendation does not unpublish or change product content",
                   all(before.get(key) == after.get(key) for key in unchanged) and after["status"] == "published")
        self.r23_boost(before, 100, status=409)
        private_fields = {"recommendation_boost", "recommendation_version"}
        public = self.call("anonymous", "GET", f"/api/v1/products/{before['id']}").data
        self.check("R23 public product hides recommendation controls", not private_fields.intersection(public))
        owned = self.own(self.fixtures["product_id"])
        self.check("R23 member ownership does not reveal recommendation controls", not private_fields.intersection(owned))
        reset = self.r23_boost(after, 0)
        self.check("R23 recommendation bonus can be cleared", reset["recommendation_boost"] == 0)

        query = prefix + " ranking"
        products = []
        for name, price, rank in (("A", "100.00", 100), ("B", "200.00", 150)):
            product = self.call("admin", "POST", "/api/v1/admin/products",
                                dict(self.product_input(query + name), price_amount=price), status=(200, 201)).data
            media = self.upload("admin")
            product = self.call("admin", "POST", f"/api/v1/admin/products/{product['id']}/images",
                                {"media_ids": [media], "expected_version": product["version"], "request_id": str(uuid.uuid4())}).data
            product = self.call("admin", "POST", f"/api/v1/admin/products/{product['id']}/publish",
                                {"expected_version": product["version"]}).data
            product = self.wait_published(product["id"], product)
            product = self.call("admin", "PUT", f"/api/v1/admin/products/{product['id']}/featured", {"manual_rank": rank}).data
            products.append(product)
        first, second = products
        self.check("R23 zero bonuses preserve original featured rank", self.r23_order(query, "featured") == [first["id"], second["id"]])
        second = self.r23_boost(second, 60)
        self.check("R23 featured score includes the additive bonus", self.r23_order(query, "featured") == [second["id"], first["id"]])
        self.r23_boost(second, 0)
        first = self.r23_boost(first, 100)
        for sort in ("hot", "recommended"):
            self.check("R23 bonus participates in " + sort, self.r23_order(query, sort)[0] == first["id"])
        self.check("R23 price descending remains a price sort", self.r23_order(query, "price_desc") == [second["id"], first["id"]])
        self.check("R23 newest remains chronological", self.r23_order(query, "newest") == [second["id"], first["id"]])
        self.r23_boost(first, 0)
        self.check("R23 clearing bonuses restores original featured order", self.r23_order(query, "featured") == [first["id"], second["id"]])

    def r23_concurrency(self):
        product = self.admin_product(self.fixtures["platform_product_id"])
        path = f"/api/v1/admin/products/{product['id']}/recommendation"
        def update(index):
            return self.clients["admin"].request("PATCH", path,
                {"boost": index + 1, "expected_version": product["recommendation_version"]})
        with concurrent.futures.ThreadPoolExecutor(max_workers=8) as executor:
            replies = list(executor.map(update, range(16)))
        statuses = [reply.status for reply in replies]
        self.check("R23 one of sixteen same-version recommendation writes succeeds",
                   statuses.count(200) == 1 and statuses.count(409) == 15,
                   successes=statuses.count(200), conflicts=statuses.count(409))
        after = self.admin_product(product["id"])
        self.check("R23 concurrent recommendations increment only their own version",
                   after["recommendation_version"] == product["recommendation_version"] + 1
                   and after["version"] == product["version"] and after["status"] == product["status"])
        self.r23_boost(after, 0)

    def r23_security(self):
        product = self.admin_product(self.fixtures["platform_product_id"])
        path = f"/api/v1/admin/products/{product['id']}/recommendation"
        body = {"boost": 10, "expected_version": product["recommendation_version"]}
        self.call("member", "PATCH", path, body, status=404)
        self.call("admin", "PATCH", path, body, cookie="", status=401)
        self.call("admin", "PATCH", path, body, cookie="qingye_admin_session=" + self.clients["member"].cookies["qingye_session"], status=401)
        for origin in ("", "null", self.args.public_origin):
            self.call("admin", "PATCH", path, body, origin=origin, status=403)
        for value in (-1, 101, 1.5, "10", None, True):
            self.call("admin", "PATCH", path, dict(body, boost=value), status=422)
        for invalid in ({"expected_version": body["expected_version"]}, {"boost": 0},
                        dict(body, expected_version=0), dict(body, role="admin")):
            self.call("admin", "PATCH", path, invalid, status=422)
        self.call("member", "PATCH", f"/api/v1/me/products/{self.fixtures['product_id']}",
                  {"expected_version": 1, "recommendation_boost": 100}, status=422)
        self.call("admin", "POST", "/api/v1/admin/products", dict(self.product_input("R23 rejected bonus injection"), recommendation_boost=100), status=422)
        self.check("R23 rejected writes preserve the recommendation version",
                   self.admin_product(product["id"])["recommendation_version"] == product["recommendation_version"])
        removed = self.admin_product(self.fixtures["r23_automatic_product_id"])
        self.call("admin", "DELETE", f"/api/v1/admin/products/{removed['id']}",
                  {"expected_version": removed["version"], "reason": "R23 isolated target boundary"})
        self.r23_boost(removed, 100, status=404)
