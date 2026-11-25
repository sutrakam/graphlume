-- MySQL style: back-quoted names, one statement per object.

CREATE TABLE IF NOT EXISTS `accounts` (
  `id` int(11) NOT NULL AUTO_INCREMENT,
  `owner` varchar(255) NOT NULL,
  `balance` decimal(12,2) DEFAULT '0.00',
  PRIMARY KEY (`id`)
);

CREATE TABLE `transfers` (
  `id` int(11) NOT NULL AUTO_INCREMENT,
  `from_account` int(11) NOT NULL,
  `to_account` int(11) NOT NULL,
  `amount` decimal(12,2) NOT NULL,
  PRIMARY KEY (`id`)
);

CREATE INDEX `idx_owner` ON `accounts` (`owner`);

CREATE VIEW `rich_accounts` AS
  SELECT `id`, `owner` FROM `accounts` WHERE `balance` > 1000;

CREATE TABLE `audit_log` (
  `id` int(11) NOT NULL,
  `message` text
);

INSERT INTO `accounts` (`owner`, `balance`) VALUES ('ann', 10.00), ('bob', 20.50);

UPDATE `accounts` SET `balance` = `balance` + 1 WHERE `owner` = 'ann';

DELETE FROM `audit_log` WHERE `id` < 10;

CREATE TRIGGER `trg_audit` AFTER INSERT ON `accounts`
  FOR EACH ROW INSERT INTO `audit_log` (`id`, `message`) VALUES (NEW.id, 'created');

CREATE FUNCTION `double_it`(x INT) RETURNS INT DETERMINISTIC
  RETURN x * 2;

SELECT `owner`, double_it(`balance`) FROM `accounts`;

DROP TABLE IF EXISTS `scratch`;
