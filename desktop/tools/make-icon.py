"""生成应用图标。

设计：深色圆角底 + 蓝色渐变方块 + 一个白色的「生成」菱形标记。
不追求花哨，只要在任务栏和托盘里能一眼认出来。
"""
import math
import os

from PIL import Image, ImageDraw

OUT_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "build")
SIZE = 512


def lerp(a, b, t):
    return tuple(round(x + (y - x) * t) for x, y in zip(a, b))


def rounded_mask(size, radius):
    mask = Image.new("L", (size, size), 0)
    draw = ImageDraw.Draw(mask)
    draw.rounded_rectangle((0, 0, size - 1, size - 1), radius=radius, fill=255)
    return mask


def gradient(size, top, bottom):
    grad = Image.new("RGB", (1, size))
    for y in range(size):
        grad.putpixel((0, y), lerp(top, bottom, y / max(1, size - 1)))
    return grad.resize((size, size))


def build():
    size = SIZE
    # 蓝色渐变底
    base = gradient(size, (91, 140, 255), (58, 104, 224))

    # 上面叠一层很淡的高光，让图标不那么平
    sheen = Image.new("L", (size, size), 0)
    sd = ImageDraw.Draw(sheen)
    sd.ellipse((-size * 0.35, -size * 0.75, size * 1.35, size * 0.45), fill=42)
    base = Image.composite(Image.new("RGB", (size, size), (255, 255, 255)), base, sheen)

    image = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    image.paste(base, (0, 0), rounded_mask(size, radius=int(size * 0.22)))

    draw = ImageDraw.Draw(image)

    # 中心标记：一个四角星（生成 / 火花）
    cx = cy = size / 2
    outer = size * 0.30
    inner = size * 0.093

    points = []
    for i in range(8):
        angle = math.pi / 4 * i - math.pi / 2
        r = outer if i % 2 == 0 else inner
        points.append((cx + r * math.cos(angle), cy + r * math.sin(angle)))

    draw.polygon(points, fill=(255, 255, 255, 245))

    # 右下角一个小圆点，暗示「第二个结果」
    dot_r = size * 0.058
    dx, dy = size * 0.715, size * 0.715
    draw.ellipse((dx - dot_r, dy - dot_r, dx + dot_r, dy + dot_r), fill=(255, 255, 255, 200))

    os.makedirs(OUT_DIR, exist_ok=True)

    png_path = os.path.join(OUT_DIR, "icon.png")
    image.save(png_path)

    ico_path = os.path.join(OUT_DIR, "icon.ico")
    sizes = [(256, 256), (128, 128), (64, 64), (48, 48), (32, 32), (16, 16)]
    image.save(ico_path, format="ICO", sizes=sizes)

    print("icon.png", os.path.getsize(png_path), "bytes")
    print("icon.ico", os.path.getsize(ico_path), "bytes")


if __name__ == "__main__":
    build()
