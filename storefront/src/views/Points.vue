<template>
  <div>
    <div class="card" style="margin-bottom: 16px;" v-if="level">
      <div style="display: flex; justify-content: space-between; flex-wrap: wrap; gap: 8px;">
        <div>
          <span class="muted">当前积分</span>
          <span style="font-size: 22px; font-weight: 700; margin-left: 8px; color: #4338ca;">{{ level.points }}</span>
        </div>
        <div class="muted">
          {{ level.current?.name || '普通会员' }}
          <template v-if="level.next"> · 距 {{ level.next.name }} {{ level.progress?.percent ?? 0 }}%</template>
        </div>
      </div>
      <div class="progress" style="margin-top: 10px;"><div :style="{ width: `${level.progress?.percent ?? 100}%` }"></div></div>
      <div class="muted" style="margin-top: 8px;">积分来源：充值赠送 / 消费累积（{{ level.current?.points_rule_json || '规则待配置' }}）；登录后可用积分兑换下方商品</div>
    </div>

    <div class="grid">
      <div v-for="p in products" :key="p.id" class="card">
        <router-link :to="`/product/${p.id}`">
          <img v-if="p.cover" :src="p.cover" style="width: 100%; border-radius: 6px; margin-bottom: 8px;" alt="" />
          <div style="font-weight: 600;">{{ p.name }}</div>
        </router-link>
        <div style="margin: 8px 0; color: #4338ca; font-weight: 700;">
          {{ p.points_required }} 积分
        </div>
        <div class="muted" style="margin-bottom: 8px;">库存 {{ p.stock > 0 ? p.stock : 0 }} 件</div>
        <button class="btn" :disabled="exchanging === p.id || p.stock <= 0" @click="exchange(p)">
          {{ exchanging === p.id ? '兑换中…' : p.stock <= 0 ? '已兑完' : '积分兑换' }}
        </button>
      </div>
    </div>
    <div v-if="!products.length && loaded" class="card muted" style="text-align: center;">暂无积分商城商品</div>
    <div v-if="error" class="card error">{{ error }}</div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue';
import { useRouter } from 'vue-router';
import { listProducts, getMyLevel, createOrder, type Product, type MyLevelReply } from '@/api';
import { getToken } from '@/api/client';

const router = useRouter();
const products = ref<Product[]>([]);
const level = ref<MyLevelReply | null>(null);
const loaded = ref(false);
const error = ref('');
const exchanging = ref<number>(0);

onMounted(async () => {
  const { data, error: err } = await listProducts({ points_only: true, page: 1, page_size: 48 });
  products.value = data?.items || [];
  error.value = err || '';
  loaded.value = true;
  if (getToken()) {
    const l = await getMyLevel();
    level.value = l.data;
  }
});

// 积分兑换（P3-01：use_points 下单 → 服务端同事务扣积分 → 直落 paid → 取货页交付）
async function exchange(p: Product) {
  if (!getToken()) {
    router.push({ path: '/login', query: { redirect: '/points' } });
    return;
  }
  if (!confirm(`确认用 ${p.points_required} 积分兑换「${p.name}」？`)) return;
  exchanging.value = p.id;
  const { data, error: err } = await createOrder({
    items: [{ product_id: p.id, quantity: 1 }],
    use_points: true
  });
  exchanging.value = 0;
  if (err || !data) {
    alert(err || '兑换失败（积分不足？）');
    return;
  }
  alert('兑换成功，请前往取货页领取卡密');
  router.push('/fetch');
  // 刷新积分
  if (getToken()) {
    const l = await getMyLevel();
    level.value = l.data;
  }
}
</script>
