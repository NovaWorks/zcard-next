<script setup lang="ts">
// 秒杀 + 促销管理（coupon:write 超管专属；与卡密/购物车同锁防超卖）。
import { onMounted, ref, h } from "vue";
import { NButton, NDataTable, NDatePicker, NInput, NInputNumber, NModal, NForm, NFormItem, NPopconfirm, NSelect } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { fetchFlashSales, createFlashSale, deleteFlashSale, fetchPromotions, upsertPromotion } from "@/service/api";
import { checkAuth } from "@/directives";
import { formatMoney } from "@/utils/money";

defineOptions({ name: "FlashTab" });

// ── 秒杀 ──
const flashLoading = ref(false);
const flashes = ref<any[]>([]);
const showFlash = ref(false);
const flashSaving = ref(false);
const flashForm = ref({ product_id: null as number | null, flash_priceYuan: 0, range: [Date.now(), Date.now() + 86400000] as [number, number], limit_qty: 10, per_user_limit: 1 });

const flashColumns: DataTableColumns<any> = [
  { title: "商品ID", key: "product_id", width: 76 },
  { title: "秒杀价", key: "flash_price", width: 90, render: (row) => formatMoney(row.flash_price) },
  { title: "开始", key: "start_at", width: 150, render: (row) => new Date(row.start_at * 1000).toLocaleString() },
  { title: "结束", key: "end_at", width: 150, render: (row) => new Date(row.end_at * 1000).toLocaleString() },
  { title: "限量", key: "limit_qty", width: 60 },
  { title: "已售", key: "sold_qty", width: 60 },
  { title: "每人限购", key: "per_user_limit", width: 76 },
  {
    title: "操作",
    key: "actions",
    width: 70,
    render: (row) =>
      checkAuth("coupon:write")
        ? h(NPopconfirm, { onPositiveClick: () => handleDeleteFlash(row.id) }, { trigger: () => h(NButton, { size: "tiny", type: "error", quaternary: true }, { default: () => "删除" }), default: () => "确定删除该秒杀？" })
        : null,
  },
];

// ── 促销 ──
const promoLoading = ref(false);
const promos = ref<any[]>([]);
const showPromo = ref(false);
const promoSaving = ref(false);
const promoForm = ref({ name: "", scope_json: "", type: "percent", thresholdYuan: 0, discount: 9500, special_priceYuan: 0, range: [Date.now(), Date.now() + 86400000 * 7] as [number, number], enabled: true });

const promoColumns: DataTableColumns<any> = [
  { title: "名称", key: "name", width: 140, ellipsis: { tooltip: true } },
  { title: "范围", key: "scope_json", width: 160, ellipsis: true, render: (row) => row.scope_json || "全场" },
  {
    title: "规则",
    key: "rule",
    width: 180,
    render: (row) =>
      row.type === "fixed"
        ? `满 ${formatMoney(row.threshold)} 减 ${formatMoney(row.discount)}`
        : row.type === "percent"
          ? `满 ${formatMoney(row.threshold)} 打 ${(row.discount / 100).toFixed(1)}折`
          : `特价 ${formatMoney(row.special_price)}`,
  },
  { title: "开始", key: "start_at", width: 150, render: (row) => new Date(row.start_at * 1000).toLocaleString() },
  { title: "结束", key: "end_at", width: 150, render: (row) => new Date(row.end_at * 1000).toLocaleString() },
  {
    title: "状态",
    key: "enabled",
    width: 70,
    render: (row) => (row.enabled ? "启用" : "停用"),
  },
];

async function loadFlash() {
  flashLoading.value = true;
  try {
    const { data, error } = await fetchFlashSales();
    if (!error && data) flashes.value = (data as any).items || [];
  } finally {
    flashLoading.value = false;
  }
}

async function loadPromos() {
  promoLoading.value = true;
  try {
    const { data, error } = await fetchPromotions();
    if (!error && data) promos.value = (data as any).promotions || (data as any).items || [];
  } finally {
    promoLoading.value = false;
  }
}

async function handleFlash() {
  if (!flashForm.value.product_id) return;
  flashSaving.value = true;
  try {
    const { error } = await createFlashSale({
      product_id: flashForm.value.product_id,
      flash_price: Math.round(flashForm.value.flash_priceYuan * 100),
      start_at: Math.floor(flashForm.value.range[0] / 1000),
      end_at: Math.floor(flashForm.value.range[1] / 1000),
      limit_qty: flashForm.value.limit_qty,
      per_user_limit: flashForm.value.per_user_limit,
    });
    if (!error) {
      window.$message?.success("秒杀已创建（与卡密库存同锁防超卖）");
      showFlash.value = false;
      loadFlash();
    }
  } finally {
    flashSaving.value = false;
  }
}

async function handleDeleteFlash(id: number) {
  const { error } = await deleteFlashSale(id);
  if (!error) {
    window.$message?.success("已删除");
    loadFlash();
  }
}

async function handlePromo() {
  if (!promoForm.value.name) return;
  promoSaving.value = true;
  try {
    const { error } = await upsertPromotion({
      name: promoForm.value.name,
      scope_json: promoForm.value.scope_json || undefined,
      type: promoForm.value.type,
      threshold: Math.round(promoForm.value.thresholdYuan * 100),
      discount: promoForm.value.type === "percent" ? promoForm.value.discount : Math.round(promoForm.value.discount),
      special_price: Math.round(promoForm.value.special_priceYuan * 100),
      start_at: Math.floor(promoForm.value.range[0] / 1000),
      end_at: Math.floor(promoForm.value.range[1] / 1000),
      enabled: promoForm.value.enabled,
    });
    if (!error) {
      window.$message?.success("促销已保存");
      showPromo.value = false;
      loadPromos();
    }
  } finally {
    promoSaving.value = false;
  }
}

onMounted(() => {
  loadFlash();
  loadPromos();
});
</script>

<template>
  <div class="flex flex-col gap-16px">
    <div>
      <div class="mb-8px flex items-center gap-8px">
        <span class="text-13px font-500">限时秒杀</span>
        <NButton v-if="checkAuth('coupon:write')" size="tiny" type="primary" @click="showFlash = true">新建秒杀</NButton>
      </div>
      <NDataTable :columns="flashColumns" :data="flashes" :loading="flashLoading" size="small"  :max-height="540" />
    </div>
    <div>
      <div class="mb-8px flex items-center gap-8px">
        <span class="text-13px font-500">满减/折扣促销（与会员折扣、券按管线顺序叠加）</span>
        <NButton v-if="checkAuth('coupon:write')" size="tiny" type="primary" @click="showPromo = true">新建促销</NButton>
      </div>
      <NDataTable :columns="promoColumns" :data="promos" :loading="promoLoading" size="small"  :max-height="540" />
    </div>

    <NModal v-model:show="showFlash" preset="dialog" title="新建秒杀" style="width: 480px">
      <NForm :model="flashForm" label-placement="left" label-width="88">
        <NFormItem label="商品ID" required>
          <NInputNumber v-model:value="flashForm.product_id" :min="1" class="w-full" />
        </NFormItem>
        <NFormItem label="秒杀价(元)" required>
          <NInputNumber v-model:value="flashForm.flash_priceYuan" :min="0.01" :precision="2" class="w-full" />
        </NFormItem>
        <NFormItem label="时间窗" required>
          <NDatePicker v-model:value="flashForm.range" type="datetimerange" clearable />
        </NFormItem>
        <NFormItem label="限量" required>
          <NInputNumber v-model:value="flashForm.limit_qty" :min="1" class="w-full" />
        </NFormItem>
        <NFormItem label="每人限购">
          <NInputNumber v-model:value="flashForm.per_user_limit" :min="1" class="w-full" />
        </NFormItem>
      </NForm>
      <template #action>
        <NButton @click="showFlash = false">取消</NButton>
        <NButton type="primary" :loading="flashSaving" @click="handleFlash">创建</NButton>
      </template>
    </NModal>

    <NModal v-model:show="showPromo" preset="dialog" title="新建促销" style="width: 520px">
      <NForm :model="promoForm" label-placement="left" label-width="88">
        <NFormItem label="名称" required>
          <NInput v-model:value="promoForm.name" />
        </NFormItem>
        <NFormItem label="范围JSON">
          <NInput v-model:value="promoForm.scope_json" placeholder='{"product_ids":[1,2]} 或 {"category_ids":[3]}，空=全场' />
        </NFormItem>
        <NFormItem label="类型">
          <NSelect v-model:value="promoForm.type" :options="[{ label: '折扣（万分比）', value: 'percent' }, { label: '满减（分）', value: 'fixed' }, { label: '特价（分）', value: 'special_price' }]" />
        </NFormItem>
        <NFormItem label="门槛(元)">
          <NInputNumber v-model:value="promoForm.thresholdYuan" :min="0" class="w-full" />
        </NFormItem>
        <NFormItem v-if="promoForm.type === 'percent'" label="折扣(万分比)">
          <NInputNumber v-model:value="promoForm.discount" :min="1" :max="10000" class="w-full" />
        </NFormItem>
        <NFormItem v-else-if="promoForm.type === 'fixed'" label="减额(元)">
          <NInputNumber v-model:value="promoForm.special_priceYuan" :min="0" class="w-full" @update:value="(v: number | null) => (promoForm.discount = Math.round((v || 0) * 100))" />
        </NFormItem>
        <NFormItem v-else label="特价(元)">
          <NInputNumber v-model:value="promoForm.special_priceYuan" :min="0" class="w-full" />
        </NFormItem>
        <NFormItem label="时间窗" required>
          <NDatePicker v-model:value="promoForm.range" type="datetimerange" clearable />
        </NFormItem>
      </NForm>
      <template #action>
        <NButton @click="showPromo = false">取消</NButton>
        <NButton type="primary" :loading="promoSaving" @click="handlePromo">保存</NButton>
      </template>
    </NModal>
  </div>
</template>
