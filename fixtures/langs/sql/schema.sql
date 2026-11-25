-- Fixture schema. DDL objects are definitions; columns, queries and
-- statements that only use an object are not.
CREATE SCHEMA shop;

CREATE TABLE customers (
    id integer PRIMARY KEY,
    email varchar(120) NOT NULL,
    created_at timestamp DEFAULT now()
);

CREATE TABLE IF NOT EXISTS shop.orders (
    id integer PRIMARY KEY,
    customer_id integer REFERENCES customers (id),
    total numeric(10, 2) NOT NULL
);

CREATE TABLE "Line Items" (
    order_id integer,
    sku text
);

CREATE TEMPORARY TABLE scratch (n integer);

CREATE TABLE copy_of_orders AS SELECT * FROM shop.orders;

CREATE VIEW active_customers AS
    SELECT id, email FROM customers WHERE created_at > now() - interval '30 days';

CREATE OR REPLACE VIEW shop.order_totals AS
    SELECT customer_id, sum(total) AS spent FROM shop.orders GROUP BY customer_id;

CREATE MATERIALIZED VIEW monthly_sales AS
    SELECT date_trunc('month', created_at) AS month, count(*) FROM customers GROUP BY 1;

CREATE INDEX idx_customers_email ON customers (email);

CREATE UNIQUE INDEX uq_orders_customer ON shop.orders (customer_id, id);

CREATE SEQUENCE order_seq START 1000;

CREATE TYPE mood AS ENUM ('sad', 'ok', 'happy');

CREATE TYPE address AS (street text, city text);

CREATE FUNCTION add_one(x integer) RETURNS integer AS $$
BEGIN
    RETURN x + 1;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION shop.total_for(cid integer) RETURNS numeric AS $$
    SELECT sum(total) FROM shop.orders WHERE customer_id = cid;
$$ LANGUAGE sql;

CREATE PROCEDURE archive_orders(before_date date)
LANGUAGE sql
AS $$
    DELETE FROM shop.orders WHERE id < 0;
$$;

CREATE FUNCTION touch_row() RETURNS trigger AS $$
BEGIN
    NEW.created_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_touch BEFORE INSERT ON customers
    FOR EACH ROW EXECUTE FUNCTION touch_row();

ALTER TABLE customers ADD COLUMN phone text;

DROP TABLE scratch;
DROP VIEW IF EXISTS old_view;
DROP INDEX idx_old;
GRANT SELECT ON customers TO reporting;
GRANT CREATE ON SCHEMA shop TO reporting;
INSERT INTO customers (id, email) VALUES (1, 'a@example.com');
UPDATE customers SET email = 'b@example.com' WHERE id = 1;
SELECT c.id, o.total FROM customers c JOIN shop.orders o ON o.customer_id = c.id;
