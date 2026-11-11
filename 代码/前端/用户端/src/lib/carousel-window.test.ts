import { describe, expect, it } from 'vitest';
import { carouselWindow, preloadCarouselSlide } from './carousel-window';
describe('大量手工轮播的导航与预载', () => {
  it('30张轮播最多显示5个邻近导航，首尾索引不越界', () => {
    expect(carouselWindow(30, 0)).toEqual([0, 1, 2, 3, 4]);
    expect(carouselWindow(30, 16)).toEqual([14, 15, 16, 17, 18]);
    expect(carouselWindow(30, 29)).toEqual([25, 26, 27, 28, 29]);
  });
  it('首尾环绕也只预载当前及两个相邻图片', () => {
    expect(
      Array.from({ length: 30 }, (_, i) => i).filter((i) => preloadCarouselSlide(i, 0, 30)),
    ).toEqual([0, 1, 29]);
    expect(
      Array.from({ length: 30 }, (_, i) => i).filter((i) => preloadCarouselSlide(i, 29, 30)),
    ).toEqual([0, 28, 29]);
  });
});
