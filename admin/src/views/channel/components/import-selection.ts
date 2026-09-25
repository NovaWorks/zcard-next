export interface SelectableProduct {
  code: string;
  name: string;
  already_imported?: boolean;
  is_locked?: boolean;
}
export interface ProductGroup<T extends SelectableProduct> {
  code: string;
  name: string;
  products: T[];
}
export function filterGroups<T extends SelectableProduct>(
  groups: ProductGroup<T>[],
  keyword: string,
) {
  const q = keyword.trim().toLocaleLowerCase();
  return groups
    .map((g) => ({
      ...g,
      products:
        !q || g.name.toLocaleLowerCase().includes(q)
          ? g.products
          : g.products.filter((p) => p.name.toLocaleLowerCase().includes(q)),
    }))
    .filter((g) => g.products.length);
}
export function selectProducts<T extends SelectableProduct>(
  selected: string[],
  products: T[],
  on = true,
) {
  const result = new Set(selected);
  for (const p of products) {
    if (on && !p.is_locked) result.add(p.code);
    else result.delete(p.code);
  }
  return [...result];
}
export interface CategoryRule {
  keywords: string[];
  excludes: string[];
  category_id: number;
  match_all: boolean;
}
export function matchedRule(name: string, rules: CategoryRule[]) {
  const text = name.normalize("NFKC").toLocaleLowerCase();
  return rules.findIndex(
    (r) =>
      r.category_id > 0 &&
      r.keywords.length > 0 &&
      !r.excludes.some((k) => text.includes(k.normalize("NFKC").toLocaleLowerCase())) &&
      (r.match_all
        ? r.keywords.every((k) => text.includes(k.normalize("NFKC").toLocaleLowerCase()))
        : r.keywords.some((k) => text.includes(k.normalize("NFKC").toLocaleLowerCase()))),
  );
}
