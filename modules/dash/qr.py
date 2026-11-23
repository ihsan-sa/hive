"""A QR code for a short text (an address), with nothing outside the standard library (E122: Settings › This device shows one
for the address a phone can reach). Byte mode, error correction level M, versions 1-10 (up to 213 bytes), the mask with the
lowest ISO 18004 penalty. matrix(text) gives rows of "0"/"1" (1 is dark) without the quiet zone; the page draws them.
The steps follow the public QR specification as laid out in Project Nayuki's reference encoder (MIT)."""

# Level M, versions 1-10: error correction codewords per block, and the number of blocks.
ECC_PER_BLOCK = (None, 10, 16, 26, 18, 24, 16, 18, 22, 22, 26)
BLOCKS = (None, 1, 1, 1, 2, 2, 4, 4, 4, 5, 5)
MAX_VERSION = 10


def _raw_modules(ver):
    """Modules left for data and error correction once the function patterns are drawn."""
    n = (16 * ver + 128) * ver + 64
    if ver >= 2:
        k = ver // 7 + 2
        n -= (25 * k - 10) * k - 55
        if ver >= 7: n -= 36
    return n


def _data_codewords(ver):
    return _raw_modules(ver) // 8 - ECC_PER_BLOCK[ver] * BLOCKS[ver]


def _gf_mul(x, y):
    z = 0
    for i in range(7, -1, -1):
        z = (z << 1) ^ ((z >> 7) * 0x11D)
        z ^= ((y >> i) & 1) * x
    return z


def _rs_divisor(degree):
    out = [0] * (degree - 1) + [1]
    root = 1
    for _ in range(degree):
        for j in range(degree):
            out[j] = _gf_mul(out[j], root)
            if j + 1 < degree: out[j] ^= out[j + 1]
        root = _gf_mul(root, 0x02)
    return out


def _rs_remainder(data, divisor):
    out = [0] * len(divisor)
    for b in data:
        factor = b ^ out.pop(0)
        out.append(0)
        for i, c in enumerate(divisor): out[i] ^= _gf_mul(c, factor)
    return out


def _codewords(data, ver):
    """Data codewords (byte segment, terminator, padding), then each block's error correction, interleaved."""
    bits = [0, 1, 0, 0]   # byte mode
    count_bits = 8 if ver <= 9 else 16
    bits += [(len(data) >> i) & 1 for i in range(count_bits - 1, -1, -1)]
    for b in data: bits += [(b >> i) & 1 for i in range(7, -1, -1)]
    cap = _data_codewords(ver) * 8
    bits += [0] * min(4, cap - len(bits))
    bits += [0] * (-len(bits) % 8)
    words = [int("".join(map(str, bits[i:i + 8])), 2) for i in range(0, len(bits), 8)]
    pad = 0xEC
    while len(words) < cap // 8: words.append(pad); pad ^= 0xEC ^ 0x11
    nb, ecl = BLOCKS[ver], ECC_PER_BLOCK[ver]
    raw = _raw_modules(ver) // 8
    short_n, short_len = nb - raw % nb, raw // nb
    div = _rs_divisor(ecl)
    blocks, k = [], 0
    for i in range(nb):
        d = words[k:k + short_len - ecl + (0 if i < short_n else 1)]
        k += len(d)
        e = _rs_remainder(d, div)
        if i < short_n: d = d + [0]
        blocks.append(d + e)
    out = []
    for i in range(len(blocks[0])):
        for j, blk in enumerate(blocks):
            if i != short_len - ecl or j >= short_n: out.append(blk[i])
    return out


def _align_positions(ver, size):
    if ver == 1: return []
    k = ver // 7 + 2
    step = (ver * 8 + k * 3 + 5) // (k * 4 - 4) * 2
    pos = [6] + [size - 7 - i * step for i in range(k - 1)][::-1]
    return pos


MASKS = (lambda x, y: (x + y) % 2 == 0, lambda x, y: y % 2 == 0, lambda x, y: x % 3 == 0, lambda x, y: (x + y) % 3 == 0,
         lambda x, y: (x // 3 + y // 2) % 2 == 0, lambda x, y: x * y % 2 + x * y % 3 == 0,
         lambda x, y: (x * y % 2 + x * y % 3) % 2 == 0, lambda x, y: ((x + y) % 2 + x * y % 3) % 2 == 0)


class _Grid:
    def __init__(self, ver):
        self.ver, self.size = ver, ver * 4 + 17
        self.m = [[False] * self.size for _ in range(self.size)]
        self.fn = [[False] * self.size for _ in range(self.size)]

    def put(self, x, y, dark):
        self.m[y][x] = bool(dark); self.fn[y][x] = True

    def functions(self):
        n = self.size
        for i in range(n): self.put(6, i, i % 2 == 0); self.put(i, 6, i % 2 == 0)
        for cx, cy in ((3, 3), (n - 4, 3), (3, n - 4)):
            for dy in range(-4, 5):
                for dx in range(-4, 5):
                    x, y = cx + dx, cy + dy
                    if 0 <= x < n and 0 <= y < n: self.put(x, y, max(abs(dx), abs(dy)) not in (2, 4))
        pos = _align_positions(self.ver, n); last = len(pos) - 1
        for i, a in enumerate(pos):
            for j, b in enumerate(pos):
                if (i, j) in ((0, 0), (0, last), (last, 0)): continue
                for dy in range(-2, 3):
                    for dx in range(-2, 3): self.put(a + dx, b + dy, max(abs(dx), abs(dy)) != 1)
        self.format_bits(0)
        if self.ver >= 7:
            rem = self.ver
            for _ in range(12): rem = (rem << 1) ^ ((rem >> 11) * 0x1F25)
            bits = self.ver << 12 | rem
            for i in range(18):
                d = (bits >> i) & 1; a, b = n - 11 + i % 3, i // 3
                self.put(a, b, d); self.put(b, a, d)

    def format_bits(self, mask):
        data = (0 << 3) | mask   # level M's format bits are 00
        rem = data
        for _ in range(10): rem = (rem << 1) ^ ((rem >> 9) * 0x537)
        bits = (data << 10 | rem) ^ 0x5412
        bit = lambda i: (bits >> i) & 1
        n = self.size
        for i in range(6): self.put(8, i, bit(i))
        self.put(8, 7, bit(6)); self.put(8, 8, bit(7)); self.put(7, 8, bit(8))
        for i in range(9, 15): self.put(14 - i, 8, bit(i))
        for i in range(8): self.put(n - 1 - i, 8, bit(i))
        for i in range(8, 15): self.put(8, n - 15 + i, bit(i))
        self.put(8, n - 8, 1)   # the dark module

    def place(self, words):
        n, i, total = self.size, 0, len(words) * 8
        right = n - 1
        while right >= 1:
            if right == 6: right = 5
            for vert in range(n):
                for j in range(2):
                    x = right - j
                    y = n - 1 - vert if ((right + 1) & 2) == 0 else vert
                    if not self.fn[y][x] and i < total:
                        self.m[y][x] = bool((words[i >> 3] >> (7 - (i & 7))) & 1); i += 1
            right -= 2

    def masked(self, k):
        f = MASKS[k]
        return [[self.m[y][x] ^ (not self.fn[y][x] and f(x, y)) for x in range(self.size)] for y in range(self.size)]


def _penalty(m):
    n, score = len(m), 0
    lines = m + [list(c) for c in zip(*m)]
    for line in lines:   # runs of five or more alike
        run = 1
        for a, b in zip(line, line[1:]):
            if a == b: run += 1
            else:
                if run >= 5: score += run - 2
                run = 1
        if run >= 5: score += run - 2
    for y in range(n - 1):   # 2x2 blocks alike
        for x in range(n - 1):
            if m[y][x] == m[y][x + 1] == m[y + 1][x] == m[y + 1][x + 1]: score += 3
    pat = (1, 0, 1, 1, 1, 0, 1)
    for line in lines:   # finder-like 1:1:3:1:1 with four light modules on one side (outside the grid counts as light)
        p = [0] * 4 + [int(v) for v in line] + [0] * 4
        for s in range(len(p) - 6):
            if tuple(p[s:s + 7]) == pat and ((s >= 4 and p[s - 4:s] == [0] * 4) or p[s + 7:s + 11] == [0] * 4): score += 40
    dark = sum(map(sum, m)); total = n * n
    score += abs(dark * 100 // total - 50) // 5 * 10   # dark share away from half
    return score


def matrix(text):
    """Rows of "0"/"1" for text, or ValueError when it is too long for version 10 at level M."""
    data = text.encode("utf-8")
    ver = next((v for v in range(1, MAX_VERSION + 1) if 4 + (8 if v <= 9 else 16) + 8 * len(data) <= _data_codewords(v) * 8), None)
    if ver is None: raise ValueError(f"too long for a QR code here ({len(data)} bytes)")
    g = _Grid(ver); g.functions(); g.place(_codewords(data, ver))
    best = None
    for k in range(8):
        g.format_bits(k)
        m = g.masked(k)
        s = _penalty(m)
        if best is None or s < best[0]: best = (s, m)
    return ["".join("1" if v else "0" for v in row) for row in best[1]]
