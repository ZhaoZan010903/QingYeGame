import base64
from copy import deepcopy
from decimal import Decimal
import hashlib
from pathlib import Path
import re
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import prepare_production_data as migration


ADMIN = "00000000-0000-0000-0000-000000000099"


def row(table, **values):
    return {column: values.get(column) for column in migration.COLUMNS[table]}


def source_fixture():
    tables = {table: [] for table in migration.COLUMNS}
    tables["categories"] = [row("categories", id="category", name="QQ炫舞", enabled=True,
                                trade_type="all", price_units=[], filter_config=[], sort_order=0)]
    tables["products"] = [row("products", id="product", legacy_id="legacy-product", product_code="QYCH17",
                              owner_id="test-member", category_id="category", title="标题", trade_type="sale",
                              price_amount=Decimal("1234567890.19"), price_unit="account", attributes={"decimal": Decimal("0.12345678901234567890")},
                              description="", status="published", source="legacy", version=4, approved_version=4,
                              review_note="test reviewer notes", rental_quotes=[], deleted_by_admin=False,
                              recommendation_boost=0, recommendation_version=1)]
    tables["media_files"] = [row("media_files", id="media", owner_id="test-admin", storage_key="legacy/" + "a" * 64,
                                 status="ready", byte_size=123, sha256="b" * 64, purpose="product", lifecycle_managed=False,
                                 upload_token_hash="old-test-ticket", upload_expires_at="old-expiry", processing_token="old-lease")]
    tables["product_images"] = [row("product_images", product_id="product", media_id="media", sort_order=2)]
    tables["content_pages"] = [row("content_pages", slug=slug, title=slug, body="test agreement", published=True, version=2)
                               for slug in ("terms", "privacy")]
    tables["site_settings"] = [row("site_settings", key="home_presentation", value={"slides": [{"productId": "product", "imageId": "media"}]})]
    tables["product_dictionary_groups"], tables["product_dictionary_options"] = migration.initial_dictionaries().values()
    schema = []
    for table in migration.ALL_TABLES:
        for index, column in enumerate(migration.COLUMNS.get(table, ["id"]), 1):
            schema.append({"table": table, "column": column, "position": index, "type": "text", "nullable": "YES", "default": None})
    return {"source_database": "qingye_test", "migrations": migration.MIGRATIONS, "schema": schema,
            "tables": tables, "checks": {"managed_objects": 0, "retired_keys": 0, "unverified_product_replicas": 0}}


def bundle_fixture():
    return migration.prepare_bundle(source_fixture(), expected_products=1)


class ProductionDataTests(unittest.TestCase):
    def test_streaming_encoding_and_sql_preserve_exact_small_fixture_bytes(self):
        bundle = bundle_fixture()
        self.assertEqual("".join(migration.encoded_chunks(bundle)), migration.encoded(bundle))
        self.assertEqual(migration.digest(bundle), hashlib.sha256(migration.encoded(bundle).encode()).hexdigest())
        self.assertEqual("\n".join(migration.iter_import_statements(bundle, ADMIN, True)), migration.import_statement(bundle, ADMIN, True))

    def test_source_revalidation_is_read_only_bounded_and_checks_normalized_rows(self):
        statements = list(migration.iter_source_verification_statements(bundle_fixture()))
        sql = "\n".join(statements)
        self.assertIn("REPEATABLE READ READ ONLY", sql)
        self.assertIn("source row count differs: media_files", sql)
        self.assertIn("source table differs: product_images", sql)
        self.assertIn("source media verification changed", sql)
        self.assertIn("NULL::text AS upload_token_hash", sql)
        self.assertIn("p.approved_version=p.version", sql)
        self.assertNotRegex(sql, r"\b(?:INSERT|UPDATE|DELETE|TRUNCATE|LOCK TABLE)\b")
        for table in ("users", "sessions", "sms_challenges", "chat_messages", "favorites", "product_views_daily"):
            self.assertNotRegex(sql, r"FROM " + table + r"\b")

    def test_validation_snapshot_bounds_row_aggregation_and_handles_sequences(self):
        sql = migration.validation_snapshot_statement()
        self.assertIn("REPEATABLE READ READ ONLY", sql)
        self.assertIn("string_agg(h,E'\\n' ORDER BY h)", sql)
        self.assertIn("jsonb_build_object('last_value',last_value,'is_called',is_called) FROM product_sale_code_seq", sql)
        self.assertNotIn("to_jsonb(s)", sql)
        self.assertNotIn("string_agg(to_jsonb", sql)

    def test_transform_keeps_business_identity_and_decimal_precision(self):
        source = source_fixture()
        bundle = migration.prepare_bundle(source, expected_products=1)
        product = bundle["payload"]["tables"]["products"][0]
        for field in ("id", "product_code", "legacy_id", "price_amount", "attributes", "version", "approved_version", "rental_quotes"):
            self.assertEqual(product[field], source["tables"]["products"][0][field])
        self.assertEqual(product["owner_id"], migration.OWNER)
        self.assertEqual(product["review_note"], "")
        self.assertEqual(source["tables"]["products"][0]["owner_id"], "test-member")
        raw = migration.encoded(bundle)
        self.assertIn("1234567890.19", raw)
        self.assertIn("0.12345678901234567890", raw)
        self.assertNotIn("test-member", raw)
        self.assertNotIn("test-admin", raw)
        self.assertNotIn("old-test-ticket", raw)
        self.assertNotIn("old-lease", raw)
        self.assertFalse(any(page["published"] for page in bundle["payload"]["tables"]["content_pages"]))
        self.assertEqual(migration.decoded(raw), bundle)

    def test_deterministic_rows_and_hash(self):
        source = source_fixture()
        first = migration.prepare_bundle(source, expected_products=1)
        for values in source["tables"].values():
            values.reverse()
        self.assertEqual(first["sha256"], migration.prepare_bundle(source, expected_products=1)["sha256"])
        first["payload"]["tables"]["products"][0]["price_amount"] += 1
        with self.assertRaisesRegex(ValueError, "checksum"):
            migration.validate_bundle(first, expected_products=1)

    def test_source_must_match_reviewed_database_schema_and_verified_media(self):
        for mutate in (
            lambda s: s.update(source_database="qingye_production"),
            lambda s: s["migrations"].__setitem__(0, "different.sql"),
            lambda s: s["checks"].update(managed_objects=1),
            lambda s: s["checks"].update(unverified_product_replicas=1),
            lambda s: s["checks"].update(retired_keys=1),
            lambda s: s["schema"].pop(),
            lambda s: s["tables"].update(users=[]),
        ):
            source = deepcopy(source_fixture())
            mutate(source)
            with self.assertRaises(ValueError):
                migration.prepare_bundle(source, expected_products=1)

    def test_import_rows_are_bounded_and_checked_by_primary_key(self):
        rows = [{"id": str(index), "body": "x" * 3000} for index in range(1001)]
        batches = list(migration.row_batches(rows))
        self.assertEqual(sum(map(len, batches)), len(rows))
        self.assertTrue(all(len(batch) <= 500 for batch in batches))
        self.assertTrue(all(sum(len(migration.encoded(row).encode()) for row in batch) <= 512 * 1024 for batch in batches))
        with self.assertRaisesRegex(ValueError, "size budget"):
            list(migration.row_batches([{"large": "x" * (9 * 1024 * 1024)}]))
        sql = migration.import_statement(bundle_fixture(), ADMIN, apply=True)
        self.assertIn("JOIN expected_rows e ON t.product_id=e.product_id AND t.media_id=e.media_id", sql)
        self.assertNotIn("DECLARE payload jsonb", sql)

    def test_visibility_and_source_lifecycle_are_not_broadened(self):
        for field, value in (("status", "offline"), ("approved_version", 3), ("deleted_at", "now"), ("purged_at", "now"), ("deleted_by_admin", True)):
            with self.subTest(field=field):
                source = source_fixture()
                source["tables"]["products"][0][field] = value
                with self.assertRaises(ValueError):
                    migration.prepare_bundle(source, expected_products=1)
        for field, value in (("lifecycle_managed", True), ("storage_key", "r20/test/objects/any"), ("storage_key", "../outside"), ("sha256", ""), ("status", "pending")):
            with self.subTest(field=field):
                source = source_fixture()
                source["tables"]["media_files"][0][field] = value
                with self.assertRaises(ValueError):
                    migration.prepare_bundle(source, expected_products=1)

    def test_missing_foreign_references_and_omitted_images_fail(self):
        for mutate in (
            lambda s: s["tables"]["product_images"].clear(),
            lambda s: s["tables"]["product_images"].append(dict(s["tables"]["product_images"][0])),
            lambda s: s["tables"]["site_settings"][0]["value"]["slides"][0].update(productId="excluded"),
            lambda s: s["tables"]["site_settings"].append(row("site_settings", key="recommendation_state", value={})),
            lambda s: s["tables"]["products"][0].update(category_id="missing"),
        ):
            source = source_fixture()
            mutate(source)
            with self.assertRaises(ValueError):
                migration.prepare_bundle(source, expected_products=1)

    def test_contact_and_disabled_avatar_assets_preserved_with_new_ownership(self):
        source = source_fixture()
        for index, purpose in ((1, "product"), (2, "avatar")):
            source["tables"]["media_files"].append(row("media_files", id=f"support-{index}", owner_id="test-admin",
                storage_key=f"images/00000000-0000-0000-0000-{index:012d}.png", status="ready", purpose=purpose,
                byte_size=20, sha256="c" * 64, lifecycle_managed=False))
        source["tables"]["contacts"] = [row("contacts", id="contact", qr_media_id="support-1")]
        source["tables"]["avatar_library"] = [row("avatar_library", id="avatar", media_id="support-2", enabled=False, created_by="test-admin")]
        bundle = migration.prepare_bundle(source, expected_products=1)
        tables = migration.mapped_tables(bundle, ADMIN)
        self.assertEqual(tables["avatar_library"][0]["created_by"], ADMIN)
        self.assertFalse(tables["avatar_library"][0]["enabled"])
        self.assertEqual(migration.summary(bundle)["local_support_files"], 2)

    def test_private_ciphertext_requires_matching_key_fingerprint(self):
        source = source_fixture()
        source["tables"]["product_private_credentials"] = [row("product_private_credentials", product_id="product", ciphertext="\\x00112233")]
        with self.assertRaisesRegex(ValueError, "fingerprint"):
            migration.prepare_bundle(source, expected_products=1)
        key = base64.b64encode(b"k" * 32).decode()
        fingerprint = migration.credential_fingerprint({"PRODUCT_CREDENTIAL_KEY": key})
        bundle = migration.prepare_bundle(source, expected_products=1, credential_key_sha256=fingerprint)
        self.assertEqual(bundle["payload"]["tables"]["product_private_credentials"][0]["ciphertext"], "\\x00112233")
        with patch.object(migration, "environment_values", return_value={"APP_ENV": "production", "MEDIA_WORKERS_ENABLED": "false", "DATABASE_URL": "postgres://127.0.0.1:5433/qingye_production", "PRODUCT_CREDENTIAL_KEY": base64.b64encode(b"z" * 32).decode()}):
            with self.assertRaisesRegex(ValueError, "does not match"):
                migration.check_target_environment(Path("unused"), bundle)

    def test_target_environment_cannot_use_running_workers_or_wrong_database(self):
        good = {"APP_ENV": "production", "MEDIA_WORKERS_ENABLED": "false", "DATABASE_URL": "postgres://user:private@127.0.0.1:5433/qingye_production"}
        with patch.object(migration, "environment_values", return_value=good):
            migration.check_target_environment(Path("unused"), bundle_fixture())
        for key, value in (("APP_ENV", "test"), ("MEDIA_WORKERS_ENABLED", "true"), ("MEDIA_WORKERS_ENABLED", ""),
                           ("DATABASE_URL", "postgres://remote/qingye_production"), ("DATABASE_URL", "postgres://localhost/qingye_test"),
                           ("DATABASE_URL", "postgres://localhost:5432/qingye_production"),
                           ("SMS_LOGIN_ENABLED", "true"), ("SMS_REGISTER_ENABLED", "true")):
            with patch.object(migration, "environment_values", return_value=good | {key: value}):
                with self.assertRaises(ValueError):
                    migration.check_target_environment(Path("unused"), bundle_fixture())

    def test_validation_route_requires_explicit_instance(self):
        with self.assertRaises(ValueError):
            migration.database_route("qingye_production_validation")
        self.assertEqual(migration.database_route("qingye_production_validation", "test")[2], 5432)
        self.assertEqual(migration.database_route("qingye_production_validation", "production")[2], 5433)
        with self.assertRaises(ValueError):
            migration.database_route("qingye_production", "test")
        sql = migration.import_statement(bundle_fixture(), ADMIN, database="qingye_production_validation", validation_instance="production")
        self.assertIn("current_setting('port')::integer<>5433", sql)

    def test_preview_is_read_only_and_apply_locks_checks_then_inserts(self):
        bundle = bundle_fixture()
        preview = migration.import_statement(bundle, ADMIN)
        self.assertIn("SERIALIZABLE READ ONLY", preview)
        for mutation in ("INSERT INTO", "DELETE FROM", "ALTER SEQUENCE", "LOCK TABLE", "setval("):
            self.assertNotIn(mutation, preview)
        sql = migration.import_statement(bundle, ADMIN, apply=True)
        self.assertIn("SERIALIZABLE READ WRITE", sql)
        self.assertIn("LOCK TABLE", sql)
        self.assertLess(sql.index("LOCK TABLE"), sql.index("SELECT pg_advisory_xact_lock"))
        self.assertIn("resource_id='initial-production'", sql)
        self.assertIn("target database has other connected clients", sql)
        self.assertIn("target transaction lock capacity is insufficient", sql)
        self.assertIn("SET LOCAL work_mem='4MB'", sql)
        self.assertIn("target has existing account data", sql)
        self.assertIn("target has non-initial audit data", sql)
        self.assertIn("IF already THEN", sql)
        self.assertIn("ALTER SEQUENCE product_sale_code_seq RESTART WITH 18", sql)
        self.assertNotIn("setval(", sql)
        self.assertNotIn("DISABLE TRIGGER", sql)
        self.assertNotIn("TRUNCATE", sql)
        self.assertNotRegex(sql, r"(?:INSERT INTO|UPDATE|DELETE FROM) (?:users|schema_migrations|sessions|media_objects|media_legacy_replicas)\b")
        self.assertLess(sql.index("target is not fresh"), sql.index("DELETE FROM product_dictionary_options"))
        self.assertLess(sql.index("INSERT INTO media_files"), sql.index("INSERT INTO product_images"))

    def test_sql_business_text_cannot_terminate_plpgsql_body(self):
        source = source_fixture()
        source["tables"]["products"][0]["title"] = "x'; $qingye_import$; DROP TABLE products; --"
        sql = migration.import_statement(migration.prepare_bundle(source, expected_products=1), ADMIN, apply=True)
        delimiter = re.search(r"DO (\$qingye_import_[0-9a-f]+\$)", sql).group(1)
        self.assertEqual(sql.count(delimiter), 2)
        self.assertIn("x''; $qingye_import$", sql)
        with self.assertRaises(ValueError):
            migration.mapped_tables(bundle_fixture(), "admin'; DROP TABLE users; --")

    def test_export_never_reads_test_accounts_or_writes_database(self):
        sql = migration.export_statement()
        self.assertIn("REPEATABLE READ READ ONLY", sql)
        self.assertIn("p.approved_version=p.version", sql)
        self.assertIn("p.deleted_at IS NULL", sql)
        for table in ("users", "sessions", "sms_challenges", "chat_messages", "favorites", "product_views_daily"):
            self.assertNotRegex(sql, r"FROM " + table + r"\b")
        self.assertNotRegex(sql, r"\b(?:INSERT|UPDATE|DELETE|TRUNCATE)\b")

    def test_database_errors_do_not_expose_private_rows_or_credentials(self):
        result = subprocess.CompletedProcess([], 1, "", "private phone secret failing row")
        with patch.object(migration.subprocess, "run", return_value=result) as call:
            with self.assertRaisesRegex(RuntimeError, "no business data") as error:
                migration.run_psql("qingye_test", "SELECT private")
            self.assertNotIn("secret", str(error.exception))
            self.assertIn("/var/run/postgresql", call.call_args.args[0])
        with self.assertRaises(ValueError):
            migration.run_psql("other_project", "SELECT 1")
        result = subprocess.CompletedProcess([], 0, '{"ok":true}\n', "")
        with patch.object(migration.subprocess, "run", return_value=result) as call:
            migration.run_psql("qingye_production", "SELECT 1")
            self.assertIn("/usr/pgsql-16/bin/psql", call.call_args.args[0])
            self.assertIn("/run/qingye-production-postgres", call.call_args.args[0])
            self.assertIn("5433", call.call_args.args[0])
        statement = migration.do_block("RAISE EXCEPTION 'target migrations differ';")
        state = migration.guard_code("target migrations differ")
        result = subprocess.CompletedProcess([], 1, "", "ERROR:  " + state + "\nprivate row: secret phone")
        with patch.object(migration.subprocess, "run", return_value=result):
            with self.assertRaises(migration.DatabaseOperationError) as caught:
                migration.run_psql("qingye_test", statement)
            self.assertEqual(caught.exception.sqlstate, state)
            self.assertEqual(caught.exception.guard, "target migrations differ")
            self.assertNotIn("secret", str(caught.exception))

    def test_private_output_is_exclusive_and_no_overwrite(self):
        with tempfile.TemporaryDirectory() as directory:
            destination = Path(directory) / "bundle.json"
            migration.write_private(destination, {"safe": True})
            migration.ensure_private(destination)
            with self.assertRaises(ValueError):
                migration.write_private(destination, {"overwrite": True})
            self.assertEqual(migration.decoded(destination.read_text()), {"safe": True})

    def test_image_order_manifest_binds_all_image_associations_and_order(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "order.jsonl"
            order = {"media_id": "media", "product_id": "product", "sort_order": 2}
            migration.write_private(path, order)
            bundle = migration.bind_image_order(bundle_fixture(), path)
            migration.validate_bundle(bundle, expected_products=1)
            self.assertIsNotNone(bundle["payload"]["image_order_sha256"])
            self.assertEqual(migration.bind_image_order(bundle, path), bundle)
            for change in ({"sort_order": 1}, {"product_id": "other"}, {"media_id": "other"}):
                path.write_text(migration.encoded(order | change) + "\n")
                with self.assertRaisesRegex(ValueError, "differs from verified image order"):
                    migration.bind_image_order(bundle, path)
            path.write_text(migration.encoded(order) + "\n\n")
            with self.assertRaises(ValueError):
                migration.bind_image_order(bundle, path)

    def test_support_hashes_bind_actual_files_without_modifying_product_evidence(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            key = "images/00000000-0000-0000-0000-000000000001.png"
            (root / "images").mkdir()
            (root / key).write_bytes(b"synthetic-support-file")
            source = source_fixture()
            product_hash = source["tables"]["media_files"][0]["sha256"]
            source["tables"]["media_files"].append(row("media_files", id="support", storage_key=key,
                byte_size=len(b"synthetic-support-file"), sha256=None))
            migration.verify_support_files(source, root)
            self.assertRegex(source["tables"]["media_files"][-1]["sha256"], r"^[a-f0-9]{64}$")
            self.assertEqual(source["tables"]["media_files"][0]["sha256"], product_hash)
            (root / key).write_bytes(b"changed")
            with self.assertRaisesRegex(ValueError, "size differs"):
                migration.verify_support_files(source, root)


if __name__ == "__main__":
    unittest.main()
