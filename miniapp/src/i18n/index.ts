/**
 * 客人端多语词典（**界面骨架文案**）。
 *
 * ─────────────────────────────────────────────────────────────
 * ★ 界面词典 vs 后端语种注册表：两套东西，分工不同，别混
 * ─────────────────────────────────────────────────────────────
 *   - **后端语种注册表**（`GET /api/translation/langs`）是「有哪些语言」的
 *     唯一真值。前端**绝不硬编码语种清单**——新增语种 = 后端登记一行，前端零改动。
 *   - **本文件的 UI 词典**是「这些固定按钮/提示该怎么说」的翻译表，每种语言**需要一行**。
 *     它无法由后端下发，因为它是产品自己的界面文案，不是门店数据。
 *   - 二者通过**短码**（`zh`/`en`/…）对齐；注册表里出现的新语种若本表没有列，
 *     由 `t()` 的回落链兜住，页面不会崩、不会空白。
 *
 * ─────────────────────────────────────────────────────────────
 * 设计原则
 * ─────────────────────────────────────────────────────────────
 *   1. **目标语言优先**——页面所有文案都随客人选的语言切换，不留中文残影（渲染纪律 L1）。
 *   2. 只收 UI 骨架文案；菜名/分类名走服务端翻译链（真实数据，不硬编码）。
 *   3. 问店员短语保留「目标语言 + 中文」双语——客人看得懂，店员也看得懂，
 *      这才是「语言不通不再成为门槛」的实际用法。
 *
 * ★ 回落链：`t(key, lang)` = `lang → en → zh → key`。
 *   即：目标语言缺失退英语，英语再缺失退中文，连词条都不存在则直接显示 key 本身
 *   （便于开发期一眼看出漏配，而不是静默显示空白）。
 *   新增语种：在每张表补一列即可；缺列时自动走上述回落。
 */

/** 前端界面文案支持的语种短码。与后端注册表的 code 对齐（后端为准，此处仅作类型约束）。 */
export type LangCode = 'zh' | 'en' | 'ja' | 'ko' | 'th' | 'fr' | 'es' | 'de' | 'pt' | 'vi'

/** 词典一行：一个 key 对应各语种的译法。要求每个语种都齐全，缺列靠 `t()` 回落。 */
type Row = Record<LangCode, string>

/**
 * 页面骨架文案表。
 *
 * 结构：`UI[业务key][语种短码] = 译文`。
 * 调用方式：`t('menu', lang)`，**不要直接读 UI**，否则会绕过回落链。
 * 每个 key 的注释说明它出现在页面的哪个位置，方便改文案时判断影响面。
 */
const UI: Record<string, Row> = {
  // 页面标题后缀 / 空分类区标题：浏览器标签页、分享预览也会用到
  menu: {
    zh: '菜单',
    en: 'Menu',
    ja: 'メニュー',
    ko: '메뉴',
    th: 'เมนู',
    fr: 'Menu',
    es: 'Menú',
    de: 'Speisekarte',
    pt: 'Cardápio',
    vi: 'Thực đơn',
  },
  // 招牌推荐位的区块标题（仅当菜品 ≥3 时出现）
  signature: {
    zh: '本店招牌',
    en: 'House Favorites',
    ja: 'おすすめ',
    ko: '추천 메뉴',
    th: 'เมนูแนะนำ',
    fr: 'Nos spécialités',
    es: 'Especialidades',
    de: 'Empfehlungen',
    pt: 'Destaques da casa',
    vi: 'Món đặc sắc',
  },
  // 头部副标题 + 底部主按钮：评价领券（只在开了券的店显示）
  rate: {
    zh: '评价领券',
    en: 'Rate & get a coupon',
    ja: '評価してクーポンGET',
    ko: '평가하고 쿠폰 받기',
    th: 'ให้คะแนนรับคูปอง',
    fr: 'Notez et recevez un coupon',
    es: 'Opina y recibe un cupón',
    de: 'Bewerten & Gutschein sichern',
    pt: 'Avalie e ganhe um cupom',
    vi: 'Đánh giá nhận ưu đãi',
  },
  // 底部「问店员」按钮 + 问店员卡标题
  ask: {
    zh: '需要帮忙？',
    en: 'Need help?',
    ja: 'お困りですか？',
    ko: '도움이 필요하세요?',
    th: 'ต้องการความช่วยเหลือ?',
    fr: "Besoin d'aide ?",
    es: '¿Necesitas ayuda?',
    de: 'Brauchen Sie Hilfe?',
    pt: 'Precisa de ajuda?',
    vi: 'Cần giúp đỡ?',
  },
  // 菜品状态标：售罄
  soldOut: {
    zh: '售罄',
    en: 'Sold out',
    ja: '売り切れ',
    ko: '품절',
    th: 'หมด',
    fr: 'Épuisé',
    es: 'Agotado',
    de: 'Ausverkauft',
    pt: 'Esgotado',
    vi: 'Hết hàng',
  },
  // 菜品状态标：剩余份数前缀（后接数字，如「余 3」/「3 left」）
  left: {
    zh: '余',
    en: 'left',
    ja: '残り',
    ko: '남음',
    th: 'เหลือ',
    fr: 'restant',
    es: 'quedan',
    de: 'übrig',
    pt: 'restantes',
    vi: 'còn',
  },
  // 加载态整屏提示
  loading: {
    zh: '加载中…',
    en: 'Loading…',
    ja: '読み込み中…',
    ko: '불러오는 중…',
    th: 'กำลังโหลด…',
    fr: 'Chargement…',
    es: 'Cargando…',
    de: 'Wird geladen…',
    pt: 'Carregando…',
    vi: 'Đang tải…',
  },
  // WiFi 行的标签（德语用 WLAN，属正常差异）
  wifi: {
    zh: 'WiFi',
    en: 'WiFi',
    ja: 'WiFi',
    ko: 'WiFi',
    th: 'WiFi',
    fr: 'WiFi',
    es: 'WiFi',
    de: 'WLAN',
    pt: 'Wi-Fi',
    vi: 'WiFi',
  },
  // 各弹层右上角关闭按钮（含 aria-label）
  close: {
    zh: '关闭',
    en: 'Close',
    ja: '閉じる',
    ko: '닫기',
    th: 'ปิด',
    fr: 'Fermer',
    es: 'Cerrar',
    de: 'Schließen',
    pt: 'Fechar',
    vi: 'Đóng',
  },
  // 券卡底部「完成」按钮
  done: {
    zh: '完成',
    en: 'Done',
    ja: '完了',
    ko: '완료',
    th: 'เสร็จสิ้น',
    fr: 'Terminer',
    es: 'Listo',
    de: 'Fertig',
    pt: 'Concluir',
    vi: 'Xong',
  },
  // 券卡标题：感谢评价
  couponTitle: {
    zh: '感谢你的评价！',
    en: 'Thanks for your review!',
    ja: 'ご評価ありがとうございます！',
    ko: '평가해 주셔서 감사합니다!',
    th: 'ขอบคุณสำหรับการให้คะแนน!',
    fr: 'Merci pour votre avis !',
    es: '¡Gracias por tu opinión!',
    de: 'Danke für Ihre Bewertung!',
    pt: 'Obrigado pela sua avaliação!',
    vi: 'Cảm ơn bạn đã đánh giá!',
  },
  // 券卡提示：截图保存、到店出示
  couponTip: {
    zh: '截图保存，到店出示即可核销',
    en: 'Screenshot it and show it at the store',
    ja: 'スクリーンショットして店頭でご提示ください',
    ko: '캡처한 뒤 매장에서 보여주세요',
    th: 'แคปหน้าจอแล้วแสดงที่ร้าน',
    fr: 'Faites une capture et montrez-la en boutique',
    es: 'Haz una captura y muéstrala en el local',
    de: 'Screenshot speichern und im Laden vorzeigen',
    pt: 'Tire um print e mostre na loja',
    vi: 'Chụp màn hình và xuất trình tại quán',
  },
  // 券卡有效期前缀（后接本地化日期）
  validUntil: {
    zh: '有效期至',
    en: 'Valid until',
    ja: '有効期限',
    ko: '유효기간',
    th: 'ใช้ได้ถึง',
    fr: "Valable jusqu'au",
    es: 'Válido hasta',
    de: 'Gültig bis',
    pt: 'Válido até',
    vi: 'Có hiệu lực đến',
  },
  // 评价弹层标题
  rateTitle: {
    zh: '这次体验怎么样？',
    en: 'How was your visit?',
    ja: '今回の体験はいかがでしたか？',
    ko: '이번 방문은 어떠셨나요?',
    th: 'ประสบการณ์ครั้งนี้เป็นอย่างไร?',
    fr: 'Comment était votre visite ?',
    es: '¿Cómo fue tu experiencia?',
    de: 'Wie war Ihr Besuch?',
    pt: 'Como foi sua experiência?',
    vi: 'Trải nghiệm lần này thế nào?',
  },
  // 评价维度一：菜品
  q1: {
    zh: '菜品',
    en: 'Food',
    ja: '料理',
    ko: '음식',
    th: 'อาหาร',
    fr: 'Cuisine',
    es: 'Comida',
    de: 'Essen',
    pt: 'Comida',
    vi: 'Món ăn',
  },
  // 评价维度二：服务
  q2: {
    zh: '服务',
    en: 'Service',
    ja: 'サービス',
    ko: '서비스',
    th: 'บริการ',
    fr: 'Service',
    es: 'Servicio',
    de: 'Service',
    pt: 'Atendimento',
    vi: 'Phục vụ',
  },
  // 评价维度三：会再来吗（复购意愿，商家最关心的指标）
  q3: {
    zh: '还会再来吗',
    en: 'Would you come back?',
    ja: 'また来たいですか？',
    ko: '다시 방문하시겠어요?',
    th: 'จะกลับมาอีกไหม?',
    fr: 'Reviendriez-vous ?',
    es: '¿Volverías?',
    de: 'Würden Sie wiederkommen?',
    pt: 'Você voltaria?',
    vi: 'Bạn có quay lại không?',
  },
  // 评价弹层文字评论框的占位提示
  commentPh: {
    zh: '想说点什么？（可选）',
    en: 'Anything to add? (optional)',
    ja: 'ご意見があれば（任意）',
    ko: '하고 싶은 말 (선택)',
    th: 'อยากบอกอะไรไหม (ไม่บังคับ)',
    fr: 'Un commentaire ? (facultatif)',
    es: '¿Algo que añadir? (opcional)',
    de: 'Etwas hinzufügen? (optional)',
    pt: 'Algo a acrescentar? (opcional)',
    vi: 'Muốn nói thêm? (không bắt buộc)',
  },
  // 评价弹层主按钮
  submit: {
    zh: '提交并领取优惠券',
    en: 'Submit & get coupon',
    ja: '送信してクーポンを受け取る',
    ko: '제출하고 쿠폰 받기',
    th: 'ส่งและรับคูปอง',
    fr: 'Envoyer et recevoir le coupon',
    es: 'Enviar y recibir el cupón',
    de: 'Absenden & Gutschein erhalten',
    pt: 'Enviar e receber o cupom',
    vi: 'Gửi và nhận ưu đãi',
  },
  // 评价提交中按钮态
  submitting: {
    zh: '提交中…',
    en: 'Submitting…',
    ja: '送信中…',
    ko: '제출 중…',
    th: 'กำลังส่ง…',
    fr: 'Envoi…',
    es: 'Enviando…',
    de: 'Wird gesendet…',
    pt: 'Enviando…',
    vi: 'Đang gửi…',
  },
  // ★ 关键：译文缺失时的诚实标注，绝不让原文冒充目标语言（渲染纪律 L3/L4）。
  // 显示在主标题旁的小标，提醒客人「这行其实是原文，不是你的语言」。
  pending: {
    zh: '原文',
    en: 'Not translated',
    ja: '未翻訳',
    ko: '미번역',
    th: 'ยังไม่แปล',
    fr: 'Non traduit',
    es: 'Sin traducir',
    de: 'Nicht übersetzt',
    pt: 'Não traduzido',
    vi: 'Chưa dịch',
  },
  // 译文缺失时，替换「原文小字辅助行」的提示语（此时主标题已是原文，再重复原文是冗余）
  pendingHint: {
    zh: '暂无译文，以下为原文',
    en: 'No translation yet — showing the original',
    ja: '翻訳が未登録のため原文を表示しています',
    ko: '번역이 없어 원문을 표시합니다',
    th: 'ยังไม่มีคำแปล กำลังแสดงต้นฉบับ',
    fr: "Pas encore de traduction — texte original affiché",
    es: 'Aún sin traducción — se muestra el original',
    de: 'Noch keine Übersetzung — Original wird angezeigt',
    pt: 'Ainda sem tradução — exibindo o original',
    vi: 'Chưa có bản dịch — đang hiển thị nguyên văn',
  },
  // 非中文时说明「这是机器翻译」，同时兜底提示可问店员（L5 纪律）
  machineNote: {
    zh: '菜单为机器翻译，如有疑问请向店员确认',
    en: 'Machine-translated menu. Please ask our staff if anything is unclear.',
    ja: '機械翻訳のメニューです。不明な点は店員にお尋ねください。',
    ko: '기계 번역 메뉴입니다. 궁금한 점은 직원에게 문의하세요.',
    th: 'เมนูแปลด้วยระบบอัตโนมัติ หากไม่แน่ใจโปรดสอบถามพนักงาน',
    fr: 'Menu traduit automatiquement. En cas de doute, demandez au personnel.',
    es: 'Menú traducido automáticamente. Consulta al personal si tienes dudas.',
    de: 'Automatisch übersetzte Speisekarte. Bei Fragen wenden Sie sich bitte an unser Personal.',
    pt: 'Cardápio traduzido automaticamente. Em caso de dúvida, pergunte à equipe.',
    vi: 'Thực đơn dịch tự động. Nếu chưa rõ, vui lòng hỏi nhân viên.',
  },
  // 语言选择弹层标题（也用作头部语言按钮的 aria-label）
  chooseLang: {
    zh: '选择语言',
    en: 'Choose language',
    ja: '言語を選択',
    ko: '언어 선택',
    th: 'เลือกภาษา',
    fr: 'Choisir la langue',
    es: 'Elegir idioma',
    de: 'Sprache wählen',
    pt: 'Escolher idioma',
    vi: 'Chọn ngôn ngữ',
  },
  // 门店拉取失败 / 不存在时的兜底标题
  unavailable: {
    zh: '门店不可用',
    en: 'Store unavailable',
    ja: '店舗をご利用いただけません',
    ko: '매장을 이용할 수 없습니다',
    th: 'ร้านไม่พร้อมใช้งาน',
    fr: 'Établissement indisponible',
    es: 'Local no disponible',
    de: 'Geschäft nicht verfügbar',
    pt: 'Loja indisponível',
    vi: 'Cửa hàng không khả dụng',
  },
  // 门店尚无菜品时的占位提示
  emptyMenu: {
    zh: '菜单正在准备中。',
    en: 'The menu is being prepared.',
    ja: 'メニューを準備中です。',
    ko: '메뉴를 준비 중입니다.',
    th: 'กำลังเตรียมเมนู',
    fr: 'Le menu est en préparation.',
    es: 'El menú se está preparando.',
    de: 'Die Speisekarte wird vorbereitet.',
    pt: 'O cardápio está sendo preparado.',
    vi: 'Thực đơn đang được chuẩn bị.',
  },
  // 问店员卡引导语：请把下面的话给店员看
  helpTitle: {
    zh: '请向店员出示',
    en: 'Show this to our staff',
    ja: '店員にご提示ください',
    ko: '직원에게 보여주세요',
    th: 'โปรดแสดงให้พนักงานดู',
    fr: 'Montrez ceci au personnel',
    es: 'Muestra esto al personal',
    de: 'Bitte dem Personal zeigen',
    pt: 'Mostre isto à equipe',
    vi: 'Vui lòng cho nhân viên xem',
  },
  // 客人端 AI 点菜助手：标题
  aiTitle: {
    zh: 'AI 点菜助手',
    en: 'AI ordering helper',
    ja: 'AI 注文アシスタント',
    ko: 'AI 주문 도우미',
    th: 'ผู้ช่วยสั่งอาหาร AI',
    fr: 'Assistant de commande IA',
    es: 'Asistente de pedidos IA',
    de: 'KI-Bestellhilfe',
    pt: 'Assistente de pedidos IA',
    vi: 'Trợ lý gọi món AI',
  },
  // 助手欢迎语（首次打开面板时给一句开场白，降低「跟机器说话」的违和感）
  aiHello: {
    zh: '你好！我是本店的点菜助手，可以帮你介绍菜品、推荐、查价格。想吃点什么？',
    en: 'Hi! I’m the ordering helper. I can tell you about dishes, suggest what to try, and check prices. What are you in the mood for?',
    ja: 'こんにちは！注文アシスタントです。料理の紹介やおすすめ、価格案内ができます。何になさいますか？',
    ko: '안녕하세요! 주문 도우미입니다. 메뉴 안내와 추천, 가격 확인을 도와드릴게요. 무엇을 드시겠어요?',
    th: 'สวัสดีค่ะ! ฉันคือผู้ช่วยสั่งอาหาร ช่วยแนะนำเมนู ราคา และจัดการสั่งได้ค่ะ อยากทานอะไรดี?',
    fr: 'Bonjour ! Je suis l’assistant de commande. Je peux vous présenter les plats, suggérer des idées et donner les prix. Qu’est-ce qui vous tente ?',
    es: '¡Hola! Soy el asistente de pedidos. Puedo presentarte platos, sugerir opciones y decirte los precios. ¿Qué te apetece?',
    de: 'Hallo! Ich bin die KI-Bestellhilfe. Ich stelle Gerichte vor, schlage etwas vor und nenne Preise. Worauf hast du Lust?',
    pt: 'Oi! Sou o assistente de pedidos. Apresento pratos, sugiro opções e informo preços. O que você gostaria?',
    vi: 'Xin chào! Tôi là trợ lý gọi món. Tôi có thể giới thiệu món, gợi ý và báo giá. Bạn muốn ăn gì?',
  },
  // 输入框占位提示
  aiPlaceholder: {
    zh: '问问有什么菜、辣不辣、多少钱…',
    en: 'Ask about dishes, spice level, prices…',
    ja: 'メニューのこと、辛さ、価格など…',
    ko: '메뉴, 매운 정도, 가격 등을 물어보세요…',
    th: 'ถามเรื่องเมนู ความเผ็ด ราคา…',
    fr: 'Plats, niveau d’épice, prix…',
    es: 'Platos, picante, precios…',
    de: 'Gerichte, Schärfe, Preise…',
    pt: 'Pratos, pimenta, preços…',
    vi: 'Món ăn, độ cay, giá cả…',
  },
  // 发送按钮
  aiSend: {
    zh: '发送',
    en: 'Send',
    ja: '送信',
    ko: '전송',
    th: 'ส่ง',
    fr: 'Envoyer',
    es: 'Enviar',
    de: 'Senden',
    pt: 'Enviar',
    vi: 'Gửi',
  },
  // 助手思考中（等待模型/兜底）
  aiThinking: {
    zh: '思考中…',
    en: 'Thinking…',
    ja: '考え中…',
    ko: '생각 중…',
    th: 'กำลังคิด…',
    fr: 'Je réfléchis…',
    es: 'Pensando…',
    de: 'Denke nach…',
    pt: 'Pensando…',
    vi: 'Đang suy nghĩ…',
  },
  // 麦克风按钮（语音输入）的提示/无障碍标签
  aiMic: {
    zh: '按住说话',
    en: 'Hold to talk',
    ja: '長押しで話す',
    ko: '눌러서 말하기',
    th: 'กดค้างเพื่อพูด',
    fr: 'Maintenir pour parler',
    es: 'Mantén para hablar',
    de: 'Zum Sprechen halten',
    pt: 'Segure para falar',
    vi: 'Giữ để nói',
  },
  // 重新开始对话
  aiNewChat: {
    zh: '新对话',
    en: 'New chat',
    ja: '新しい会話',
    ko: '새 대화',
    th: 'สนทนาใหม่',
    fr: 'Nouvelle conversation',
    es: 'Nueva conversación',
    de: 'Neuer Chat',
    pt: 'Nova conversa',
    vi: 'Trò chuyện mới',
  },
  // 发起服务请求后的成功提示（加菜 / 加水 / 服务铃）
  aiNotify: {
    zh: '已通知服务员，请稍等',
    en: 'Staff notified — please wait',
    ja: 'スタッフに通知しました、少々お待ちください',
    ko: '직원에게 알림을 보냈습니다, 잠시만요',
    th: 'แจ้งพนักงานแล้ว ค่อยๆ รอสักครู่',
    fr: 'Personnel prévenu — un instant',
    es: 'Avisamos al personal — un momento',
    de: 'Personal benachrichtigt — einen Moment',
    pt: 'Equipe avisada — um instante',
    vi: 'Đã báo nhân viên, vui lòng chờ',
  },
  // 网络错误提示
  aiNetErr: {
    zh: '网络开了点小差，请重试',
    en: 'Network hiccup, please retry',
    ja: '通信エラー、もう一度お試しください',
    ko: '네트워크 오류, 다시 시도하세요',
    th: 'เน็ตเวิร์กขัดข้อง ลองใหม่',
    fr: 'Problème réseau, réessayez',
    es: 'Fallo de red, inténtalo de nuevo',
    de: 'Netzwerkfehler, erneut versuchen',
    pt: 'Falha de rede, tente de novo',
    vi: 'Lỗi mạng, thử lại',
  },
  // 助手面板里的「转真人店员」入口（无网兜底短语卡）
  aiStaff: {
    zh: '问真人店员',
    en: 'Talk to staff',
    ja: '店員に聞く',
    ko: '직원에게 문의',
    th: 'ถามพนักงาน',
    fr: 'Parler au personnel',
    es: 'Hablar con el personal',
    de: 'Personal fragen',
    pt: 'Falar com a equipe',
    vi: 'Hỏi nhân viên',
  },
}

/**
 * 取界面文案。
 *
 * ★ 回落链：`lang → en → zh → key`。
 *   - `lang` 命中：直接用目标语言（正常路径）。
 *   - 该语种缺列：退到英语（覆盖面最广）。
 *   - 英语也缺：退到中文（产品母语，保证不空白）。
 *   - key 根本不在表里：直接返回 key 字符串本身，便于开发期发现漏配，
 *     而不是静默返回空串导致页面出现莫名的空白按钮。
 *
 * 注意：`row[lang as LangCode]` 用断言是因为运行时 `lang` 可能来自后端注册表，
 * 含本表未收录的语种；断言不改变运行时行为，缺失时自然走 `??` 回落。
 */
export function t(key: string, lang: string): string {
  const row = UI[key]
  if (!row) return key
  return row[lang as LangCode] ?? row.en ?? row.zh ?? key
}

/** 语言选择器上的一行显示名：优先母语写法，缺失则用大写短码（如 JA）。 */
export function langLabel(lang: { code: string; native: string }): string {
  return lang.native || lang.code.toUpperCase()
}

/**
 * 问店员常用短语：目标语言在上（客人看得懂），中文在下（店员看得懂）。
 * 这是无网也能用的兜底——不依赖 WiFi、不依赖翻译服务，故硬编码在前端。
 * `zh` 字段既是中文原文，也充当短语的唯一标识（列表 key 与兜底文案）。
 */
export type Phrase = { zh: string; texts: Record<LangCode, string> }

/**
 * 问店员短语清单。覆盖点单/忌口/过敏/结账/洗手间/打包等高频场景。
 * 加短语只需往数组追加一项；加语种则在每个 `texts` 里补一列（缺列回落 en）。
 */
export const PHRASES: Phrase[] = [
  {
    zh: '请给我菜单',
    texts: {
      zh: '请给我菜单',
      en: 'Could I see the menu?',
      ja: 'メニューを見せてください',
      ko: '메뉴판 보여주세요',
      th: 'ขอดูเมนูหน่อย',
      fr: 'Puis-je voir le menu ?',
      es: '¿Me trae el menú?',
      de: 'Könnte ich die Speisekarte sehen?',
      pt: 'Pode me mostrar o cardápio?',
      vi: 'Cho tôi xem thực đơn',
    },
  },
  {
    zh: '请不要放辣',
    texts: {
      zh: '请不要放辣',
      en: 'Not spicy, please.',
      ja: '辛くしないでください',
      ko: '맵지 않게 해주세요',
      th: 'ไม่ใส่เผ็ดนะ',
      fr: 'Pas épicé, s’il vous plaît.',
      es: 'Sin picante, por favor.',
      de: 'Bitte nicht scharf.',
      pt: 'Sem pimenta, por favor.',
      vi: 'Xin đừng cho cay',
    },
  },
  {
    zh: '我对花生过敏',
    texts: {
      zh: '我对花生过敏',
      en: 'I am allergic to peanuts.',
      ja: 'ピーナッツアレルギーがあります',
      ko: '저는 땅콩 알레르기가 있어요',
      th: 'ฉันแพ้ถั่วลิสง',
      fr: 'Je suis allergique aux cacahuètes.',
      es: 'Soy alérgico al cacahuete.',
      de: 'Ich bin allergisch gegen Erdnüsse.',
      pt: 'Sou alérgico a amendoim.',
      vi: 'Tôi bị dị ứng đậu phộng',
    },
  },
  {
    zh: '请帮我结账',
    texts: {
      zh: '请帮我结账',
      en: 'The bill, please.',
      ja: 'お会計をお願いします',
      ko: '계산해 주세요',
      th: 'คิดเงินด้วย',
      fr: "L'addition, s'il vous plaît.",
      es: 'La cuenta, por favor.',
      de: 'Die Rechnung, bitte.',
      pt: 'A conta, por favor.',
      vi: 'Cho tôi thanh toán',
    },
  },
  {
    zh: '洗手间在哪里？',
    texts: {
      zh: '洗手间在哪里？',
      en: 'Where is the restroom?',
      ja: 'お手洗いはどこですか？',
      ko: '화장실이 어디예요?',
      th: 'ห้องน้ำอยู่ที่ไหน',
      fr: 'Où sont les toilettes ?',
      es: '¿Dónde está el baño?',
      de: 'Wo ist die Toilette?',
      pt: 'Onde fica o banheiro?',
      vi: 'Nhà vệ sinh ở đâu?',
    },
  },
  {
    zh: '请帮我打包',
    texts: {
      zh: '请帮我打包',
      en: 'Could you pack this to go?',
      ja: '持ち帰りでお願いします',
      ko: '포장해 주세요',
      th: 'ขอห่อกลับบ้าน',
      fr: 'À emporter, s’il vous plaît.',
      es: 'Para llevar, por favor.',
      de: 'Zum Mitnehmen, bitte.',
      pt: 'Para viagem, por favor.',
      vi: 'Cho tôi mang về',
    },
  },
]

/**
 * 取短语的目标语言写法：`lang → en → zh`（最后必落到中文原文，保证非空）。
 * 与 `t()` 的回落链同源，但终点是短语自身的中文原文，而非 key。
 */
export function phraseText(p: Phrase, lang: string): string {
  return p.texts[lang as LangCode] ?? p.texts.en ?? p.zh
}
