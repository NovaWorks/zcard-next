-- 支付方式级收银台：渠道自定义图标 + 方式列表（易支付按支付宝/微信分开收、USDT 按链选择）。
ALTER TABLE "payment_channels" ADD COLUMN "icon" varchar(500) NOT NULL DEFAULT '';
ALTER TABLE "payment_channels" ADD COLUMN "methods" json NULL;
