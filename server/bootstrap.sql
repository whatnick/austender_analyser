create database austender;
use austender;
CREATE TABLE contracts (
 CNID String,
 Agency String,
 PublishDate DateTime,
 Category String,
 ContractValue Decimal64(3),
 ContractPeriod String,
 ATMID String,
 SupplierName String
) 
ENGINE = MergeTree()
PARTITION BY toYYYYMMDD(PublishDate)
ORDER BY (PublishDate);