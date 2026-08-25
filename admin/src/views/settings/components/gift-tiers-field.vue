<script setup lang="ts">
// 充值赠送档位可视化编辑器（recharge.gift_tiers / supplier_recharge.gift_tiers）：
// 结构 [{amount, gift_balance, gift_points?}]（金额一律分存储，界面以元编辑）。
// 供货档位（supplier）无积分列；买家端档位按钮与支付到账均按此展示/入账。
import { ref, watch } from "vue";
import { NButton, NInputNumber } from "naive-ui";

interface Tier {
  amountYuan: number | null;
  giftYuan: number | null;
  giftPoints: number | null;
}

const props = defineProps<{ value: string | null; supplier?: boolean }>();
const emit = defineEmits<{ (e: "update", v: Record<string, number>[] | null): void }>();

const tiers = ref<Tier[]>([]);

function parse(v: string | null) {
  const out: Tier[] = [];
  try {
    const arr = JSON.parse(v || "null");
    if (Array.isArray(arr)) {
      for (const t of arr) {
        out.push({
          amountYuan: Number(t.amount) > 0 ? Number(t.amount) / 100 : null,
          giftYuan: Number(t.gift_balance) > 0 ? Number(t.gift_balance) / 100 : null,
          giftPoints: Number(t.gift_points) > 0 ? Number(t.gift_points) : null,
        });
      }
    }
  } catch {
    /* 非法 JSON 按空处理 */
  }
  tiers.value = out;
}
parse(props.value);
watch(() => props.value, parse);

function push() {
  const arr = tiers.value
    .filter((t) => (t.amountYuan || 0) > 0)
    .sort((a, b) => (a.amountYuan || 0) - (b.amountYuan || 0))
    .map((t) => {
      const row: Record<string, number> = {
        amount: Math.round((t.amountYuan || 0) * 100),
        gift_balance: Math.round((t.giftYuan || 0) * 100),
      };
      if (!props.supplier) row.gift_points = Math.round(t.giftPoints || 0);
      return row;
    });
  emit("update", arr.length ? arr : null);
}

function addRow() {
  tiers.value.push({ amountYuan: null, giftYuan: null, giftPoints: null });
}

function removeRow(i: number) {
  tiers.value.splice(i, 1);
  push();
}
</script>

<template>
  <div class="w-full">
    <div v-if="tiers.length" class="flex flex-col gap-6px">
      <div class="flex items-center gap-8px text-12px text-gray-400">
        <span class="w-110px text-center">单笔充值满</span>
        <span class="w-110px text-center">赠送余额</span>
        <span v-if="!supplier" class="w-110px text-center">赠送积分</span>
        <span class="w-40px" />
      </div>
      <div v-for="(t, i) in tiers" :key="i" class="flex items-center gap-8px">
        <NInputNumber v-model:value="t.amountYuan" size="small" :min="0.01" :precision="2" class="w-110px" placeholder="元" @update:value="push">
          <template #suffix>元</template>
        </NInputNumber>
        <NInputNumber v-model:value="t.giftYuan" size="small" :min="0" :precision="2" class="w-110px" placeholder="0.00" @update:value="push">
          <template #suffix>元</template>
        </NInputNumber>
        <NInputNumber v-if="!supplier" v-model:value="t.giftPoints" size="small" :min="0" class="w-110px" placeholder="0" @update:value="push">
          <template #suffix>分</template>
        </NInputNumber>
        <NButton size="tiny" quaternary type="error" class="w-40px" @click="removeRow(i)">删</NButton>
      </div>
    </div>
    <div v-else class="mb-8px text-12px text-gray-400">
      未配置赠送——买家按实付金额到账；添加档位后买家充值页会展示「充 X 送 Y」引导
    </div>
    <div class="mt-8px flex items-center gap-8px">
      <NButton size="tiny" dashed @click="addRow">+ 添加档位</NButton>
      <span v-if="!supplier" class="text-11px text-gray-400">
        想按「累计充值」自动打折/升级？到 营销管理 → 会员等级 设置升级阈值与折扣（与本档位赠送可叠加）
      </span>
      <span v-else class="text-11px text-gray-400">供货账户充值满额赠送余额，到账 = 本金 + 赠送（采购提货通用）</span>
    </div>
  </div>
</template>
