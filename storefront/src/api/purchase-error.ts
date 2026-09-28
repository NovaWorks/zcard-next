// Public messages never render plugin-provided diagnostics or private level names.
export function purchaseError(reason: unknown): string | null {
  switch (reason) {
    case 'LOGIN_REQUIRED': return '此商品需要登录后购买，请先登录。';
    case 'MEMBER_LEVEL_DENIED': return '当前账号不符合此商品的购买条件，请联系商家。';
    case 'PLUGIN_UNAVAILABLE': return '此商品的购买校验暂不可用，请稍后重试或联系商家。';
    default: return null;
  }
}
